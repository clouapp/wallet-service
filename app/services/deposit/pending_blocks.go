package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
)

var errNoPendingStore = errors.New("no pending deposit store configured")

// ReprocessResult counts what one pass over a chain's pending blocks did.
type ReprocessResult struct {
	Due          int
	Resolved     int
	StillPending int
}

// recordPending saves a block that failed every immediate retry, merging it with the
// entry already kept for that block, and raises one ERROR log per entry.
func (s *Service) recordPending(ctx context.Context, chainID string, block uint64, failure blockResult) error {
	if failure.err == nil {
		return fmt.Errorf("record pending %s block %d: no failure to record", chainID, block)
	}
	if s.pending == nil {
		return errNoPendingStore
	}
	now := s.now()
	entry, found, err := pending.Get(ctx, s.pending, chainID, block)
	if err != nil {
		slog.Warn("pending deposit lookup failed, recording a new entry", "chain", chainID, "block", block, "error", err)
		found = false
	}
	if !found {
		entry = pending.Entry{Chain: chainID, Block: block, FirstFailedAt: now}
	}
	entry.Attempts++
	if len(failure.failedTx) > 0 {
		entry.TxHashes = append([]string(nil), failure.failedTx...)
	}
	entry.ErrorClass = errorClass(failure.err)
	entry.LastError = pending.TruncateError(chain.ClientText(failure.err))
	entry.LastFailedAt = now
	entry.NextRetryAt = now.Add(s.failure.pendingBackoff(entry.Attempts))
	if err := s.pending.Put(ctx, entry); err != nil {
		return err
	}
	slog.Error("deposit block pending",
		"chain", chainID,
		"block", block,
		"attempt", entry.Attempts,
		"immediate_tries", failure.tries,
		"error_class", entry.ErrorClass,
		"tx_hashes", entry.TxHashes,
		"first_failed_at", entry.FirstFailedAt.Format(time.RFC3339),
		"next_retry_at", entry.NextRetryAt.Format(time.RFC3339),
		"error", failure.err,
		"provider_cause", chain.CauseText(failure.err),
	)
	return nil
}

// resolvePending drops the entry of a block whose deposits are now all recorded.
func (s *Service) resolvePending(ctx context.Context, chainID string, block uint64) error {
	if s.pending == nil {
		return nil
	}
	_, found, err := pending.Get(ctx, s.pending, chainID, block)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := s.pending.Delete(ctx, chainID, block); err != nil {
		return err
	}
	slog.Info("pending deposit block resolved", "chain", chainID, "block", block)
	return nil
}

// ReprocessPending retries a chain's pending blocks whose backoff has elapsed, or all
// of them with force. A block is dropped from the list only once all its deposits are
// recorded; recording is idempotent, so a block processed twice adds nothing.
func (s *Service) ReprocessPending(ctx context.Context, chainID string, force bool) (ReprocessResult, error) {
	if s.pending == nil {
		return ReprocessResult{}, nil
	}
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return ReprocessResult{}, err
	}
	entries, err := s.pending.List(ctx, chainID)
	if err != nil {
		return ReprocessResult{}, fmt.Errorf("list pending %s blocks: %w", chainID, err)
	}
	due := pending.Due(entries, s.now())
	if force {
		due = entries
	}
	result := ReprocessResult{Due: len(due)}
	for _, entry := range due {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		transfers, fetchErr := adapter.ScanBlock(ctx, entry.Block)
		outcome := s.processBlock(ctx, chainID, adapter, fetchedBlock{number: entry.Block, transfers: transfers, err: fetchErr}, adapter.ScanBlock)
		if outcome.err != nil {
			if err := s.recordPending(ctx, chainID, entry.Block, outcome); err != nil {
				slog.Error("pending deposit block failed again and its retry could not be saved; the previous entry is kept",
					"chain", chainID, "block", entry.Block, "attempt", entry.Attempts+1, "error", errors.Join(outcome.err, err))
			}
			result.StillPending++
			continue
		}
		if err := s.pending.Delete(ctx, chainID, entry.Block); err != nil {
			slog.Error("pending deposit block recovered but could not be removed; it will be processed again", "chain", chainID, "block", entry.Block, "error", err)
			result.StillPending++
			continue
		}
		slog.Info("pending deposit block recovered", "chain", chainID, "block", entry.Block, "attempts", entry.Attempts, "new_deposits", outcome.recorded)
		result.Resolved++
	}
	return result, nil
}

// ReprocessDuePending is the scan loop's pass over pending blocks whose backoff elapsed.
func (s *Service) ReprocessDuePending(ctx context.Context, chainID string) (int, error) {
	result, err := s.ReprocessPending(ctx, chainID, false)
	return result.Resolved, err
}

func (s *Service) ListPending(ctx context.Context, chainID string) ([]pending.Entry, error) {
	if s.pending == nil {
		return nil, errNoPendingStore
	}
	return s.pending.List(ctx, chainID)
}

// PendingCounts returns how many blocks are pending per registered chain.
func (s *Service) PendingCounts(ctx context.Context) (map[string]int, error) {
	if s.pending == nil {
		return nil, errNoPendingStore
	}
	counts := map[string]int{}
	var failures []error
	for _, chainID := range s.registry.ChainIDs() {
		entries, err := s.pending.List(ctx, chainID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		counts[chainID] = len(entries)
	}
	return counts, errors.Join(failures...)
}

// pendingHealthTimeout bounds the health check's read of the pending store.
const pendingHealthTimeout = 2 * time.Second

// PendingHealth is the pending-block counts the health check reports. Err
// is set when the store, or a chain's list, could not be read; Counts keeps
// what could.
type PendingHealth struct {
	Counts map[string]int
	Total  int
	Err    error
}

// PendingHealth reads PendingCounts with a short deadline and sums them. It
// never fails: pending blocks are recovered by the reprocessor, so they are
// reported, not treated as the API being down.
func (s *Service) PendingHealth(parent context.Context) PendingHealth {
	ctx, cancel := context.WithTimeout(parent, pendingHealthTimeout)
	defer cancel()
	counts, err := s.PendingCounts(ctx)
	health := PendingHealth{Counts: counts, Err: err}
	for _, count := range counts {
		health.Total += count
	}
	if err != nil {
		slog.Error("deposit scanner pending counts", "error_type", fmt.Sprintf("%T", err))
	}
	return health
}
