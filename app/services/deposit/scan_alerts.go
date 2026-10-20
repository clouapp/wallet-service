package deposit

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

const (
	// DefaultScanStaleAfter is how long a scanned chain may go without a completed
	// scan cycle before the tracker warns about it.
	DefaultScanStaleAfter = 10 * time.Minute

	scanLagMessage       = "deposit scan lag above the alert threshold"
	scanStaleMessage     = "deposit scan has not completed a cycle within the alert threshold"
	pendingBlocksMessage = "deposit blocks pending a retry"
)

// ScanAlertThresholds are the limits past which the tracker warns about a chain's
// deposit scan. LagBlocks is compared with head − checkpoint of the last completed
// cycle and StaleAfter with the time since it ended. Zero keeps the next level: a
// chain's thresholds fall back to the global ones, and those to the chain's
// catch-up window (a lag the next cycle cannot close) and DefaultScanStaleAfter.
type ScanAlertThresholds struct {
	LagBlocks  uint64
	StaleAfter time.Duration
}

// SetScanAlertThresholds sets the global thresholds and, when perChain is not nil,
// the thresholds of each chain, read on every pass.
func (s *Service) SetScanAlertThresholds(global ScanAlertThresholds, perChain func(chainID string) ScanAlertThresholds) {
	s.scanAlerts = global
	s.chainScanAlerts = perChain
}

// scanAlertThresholdsFor resolves the thresholds of chainID.
func (s *Service) scanAlertThresholdsFor(ctx context.Context, chainID string) ScanAlertThresholds {
	resolved := s.scanAlerts
	if s.chainScanAlerts != nil {
		chain := s.chainScanAlerts(chainID)
		if chain.LagBlocks > 0 {
			resolved.LagBlocks = chain.LagBlocks
		}
		if chain.StaleAfter > 0 {
			resolved.StaleAfter = chain.StaleAfter
		}
	}
	if resolved.LagBlocks == 0 {
		resolved.LagBlocks = s.scanOptionsFor(ctx, chainID).CatchUpBlocks
	}
	if resolved.StaleAfter <= 0 {
		resolved.StaleAfter = DefaultScanStaleAfter
	}
	return resolved
}

// warnScanHealth runs on every tracker pass. It logs a warning per scanned chain
// whose last completed cycle trailed the head by more than its lag threshold or
// ended longer ago than its stale threshold (the scanner stopped, or every cycle
// fails), and per chain with pending blocks. It reads the scan activity and the
// pending store only, never a chain, and fails nothing.
func (s *Service) warnScanHealth(ctx context.Context) {
	s.warnScanActivity(ctx)
	s.warnPendingBlocks(ctx)
}

func (s *Service) warnScanActivity(ctx context.Context) {
	if s.activity == nil {
		return
	}
	activity, err := s.readScanActivity(ctx)
	if err != nil {
		slog.Error("deposit scan activity unreadable; scan lag alerts skipped this pass", "error", err)
		return
	}
	now := s.now()
	for _, chainID := range sortedChains(activity) {
		scan := activity[chainID].Scan
		if scan == nil {
			continue
		}
		thresholds := s.scanAlertThresholdsFor(ctx, chainID)
		lastScannedAt := scan.At.UTC().Format(time.RFC3339)
		if lag := scan.LagBlocks(); lag > thresholds.LagBlocks {
			slog.Warn(scanLagMessage,
				"chain", chainID, "lag_blocks", lag, "threshold_blocks", thresholds.LagBlocks,
				"head", scan.Head, "checkpoint", scan.Checkpoint, "last_scanned_at", lastScannedAt)
		}
		if age := now.Sub(scan.At); age > thresholds.StaleAfter {
			slog.Warn(scanStaleMessage,
				"chain", chainID, "minutes_since_scan", int(age/time.Minute), "threshold_minutes", int(thresholds.StaleAfter/time.Minute),
				"last_scanned_at", lastScannedAt, "lag_blocks", scan.LagBlocks())
		}
	}
}

func (s *Service) warnPendingBlocks(ctx context.Context) {
	if s.pending == nil {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, pendingHealthTimeout)
	defer cancel()
	counts, err := s.PendingCounts(readCtx)
	if err != nil {
		slog.Error("pending deposit blocks unreadable for the alert", "error", err)
	}
	for _, chainID := range sortedChains(counts) {
		if count := counts[chainID]; count > 0 {
			slog.Warn(pendingBlocksMessage, "chain", chainID, "pending_blocks", count,
				"hint", "the scan retries them; scan:deposits "+chainID+" --pending lists them")
		}
	}
}

// sortedChains lists the keys of a per-chain map in order, so one pass logs the
// chains in the same order every time.
func sortedChains[V any](perChain map[string]V) []string {
	chains := make([]string, 0, len(perChain))
	for chainID := range perChain {
		chains = append(chains, chainID)
	}
	sort.Strings(chains)
	return chains
}
