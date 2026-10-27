package deposit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/macrowallets/waas/pkg/types"
)

const recentCheckpointPrefix = "recent:"

// SetRecentScanPolicy opts chains into a second, durable scan cursor while their
// historical cursor catches up. The initial window is read only when creating the
// recent cursor; a restart always resumes that cursor, never skips ahead again.
// Configure this before starting scanners. An empty list disables recent scanning.
func (s *Service) SetRecentScanPolicy(chains []string, initialWindow uint64) error {
	if len(chains) > 0 && (initialWindow == 0 || initialWindow > 10000) {
		return fmt.Errorf("recent deposit scan initial window must be between 1 and 10000 blocks")
	}
	configured := make(map[string]struct{}, len(chains))
	for _, chainID := range chains {
		if strings.TrimSpace(chainID) != chainID || chainID == "" || strings.Contains(chainID, ":") || len(chainID)+len(recentCheckpointPrefix) > 50 {
			return fmt.Errorf("recent deposit scan requires a nonempty chain ID of at most %d bytes without colons or surrounding whitespace", 50-len(recentCheckpointPrefix))
		}
		configured[chainID] = struct{}{}
	}
	s.recentChains = configured
	s.recentWindow = initialWindow
	return nil
}

func recentCheckpointKey(chainID string) string { return recentCheckpointPrefix + chainID }

// A recent scan plus backfill can outlive the fixed block-scan lock TTL on a
// throttled RPC. Renew the shared chain lock and cancel work if its ownership is
// lost, using the same hold as the signature scanner.
func (s *Service) lockScanCycle(ctx context.Context, chainID string) (context.Context, func(), error) {
	if _, enabled := s.recentChains[chainID]; !enabled {
		release, err := s.lockChain(chainID)
		return ctx, release, err
	}
	hold, err := s.holdChain(chainID)
	if err != nil {
		return ctx, nil, err
	}
	cycleCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-hold.Lost():
			cancel()
		case <-cycleCtx.Done():
		}
	}()
	return cycleCtx, func() { cancel(); hold.Release() }, nil
}

// reorgScanCheckpoint includes blocks already read by the recent scanner, even
// after that policy is disabled. A replacement payment in their new fork must be
// rescanned now rather than wait for historical backfill to reach it.
func (s *Service) reorgScanCheckpoint(ctx context.Context, chainID string) (uint64, bool, error) {
	historical, found, err := s.loadCheckpoint(ctx, chainID)
	if err != nil {
		return 0, false, err
	}
	recent, recentFound, err := s.loadCheckpoint(ctx, recentCheckpointKey(chainID))
	if err != nil {
		return 0, false, err
	}
	return max(historical, recent), found || recentFound, nil
}

// scanRecentBlocks runs under the same chain lock as backfill. It returns active
// once a recent cursor exists, and caughtUp only when that cursor covers the sampled
// head. Backfill remains the authority for historical coverage and health lag.
func (s *Service) scanRecentBlocks(ctx context.Context, chainID string, adapter types.Chain, historical, head uint64, opts ScanOptions) (active, caughtUp bool, err error) {
	if _, enabled := s.recentChains[chainID]; !enabled {
		return false, false, nil
	}
	key := recentCheckpointKey(chainID)
	cursor, found, err := s.loadCheckpoint(ctx, key)
	if err != nil {
		return true, false, fmt.Errorf("read recent deposit cursor: %w", err)
	}
	if !found {
		if head <= historical || head-historical <= opts.CatchUpBlocks {
			return false, false, nil
		}
		cursor = historical
		if head-historical > s.recentWindow {
			cursor = head - s.recentWindow
		}
		// Persist the floor before reading anything. A timeout or write failure
		// retries from here, while the historical checkpoint is left untouched.
		if err := s.saveCheckpoint(ctx, key, cursor); err != nil {
			return true, false, fmt.Errorf("initialize recent deposit cursor: %w", err)
		}
		slog.Warn("recent deposit scan enabled alongside historical backfill", "chain", chainID, "historical_checkpoint", historical, "recent_checkpoint", cursor, "head", head)
	}
	if err := checkCheckpointNotAhead(chainID, cursor, head); err != nil {
		return true, false, err
	}
	if cursor >= head {
		return true, true, nil
	}
	end := cursor + opts.window(head-cursor)
	scanned, scanErr := s.scanRange(ctx, chainID, adapter, cursor+1, end, opts.Concurrency)
	if scanned > cursor {
		if err := s.saveCheckpoint(ctx, key, scanned); err != nil {
			return true, false, fmt.Errorf("save recent deposit cursor: %w", err)
		}
	}
	if scanErr != nil {
		return true, false, fmt.Errorf("scan recent deposit blocks: %w", scanErr)
	}
	slog.Info("recent deposit scan complete", "chain", chainID, "historical_checkpoint", historical, "recent_checkpoint", scanned, "head", head)
	return true, scanned >= head, nil
}

// dropRecentCheckpoint runs under the chain lock before an operator moves the
// historical cursor or changes networks. The old network's recent cursor must not
// survive an alignment, even if this process no longer enables recent scanning.
func (s *Service) dropRecentCheckpoint(ctx context.Context, chainID string) error {
	key := recentCheckpointKey(chainID)
	if err := s.clearCachedCheckpoint(ctx, key); err != nil {
		return err
	}
	if err := s.checkpoints.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete recent deposit cursor: %w", err)
	}
	return nil
}
