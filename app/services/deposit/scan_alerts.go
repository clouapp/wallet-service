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

	// scanAlertReminder is how often a raised alert is logged again while it lasts.
	scanAlertReminder = 30 * time.Minute

	scanLagMessage              = "deposit scan lag above the alert threshold"
	scanLagClearedMessage       = "deposit scan lag back within the alert threshold"
	scanStaleMessage            = "deposit scan has not completed a cycle within the alert threshold"
	scanStaleClearedMessage     = "deposit scan completes its cycles again"
	pendingBlocksMessage        = "deposit blocks pending a retry"
	pendingBlocksClearedMessage = "no deposit block pending a retry any more"

	signatureStaleMessage         = "deposit signature sync has not completed a cycle within the alert threshold"
	signatureStaleClearedMessage  = "deposit signature sync completes its cycles again"
	signatureFailedMessage        = "deposit signature sync left accounts failed"
	signatureFailedClearedMessage = "deposit signature sync reads every account again"
)

// AlertTransition is what raising an alert did: raised it, reminded of it (it was
// raised a reminder period ago or more), or nothing (it was raised more recently).
type AlertTransition int

const (
	AlertQuiet AlertTransition = iota
	AlertRaised
	AlertReminded
)

// ScanAlertState remembers which scan alerts are raised, across passes and
// processes, so a pass logs an alert when it is raised, again every reminder period
// while it lasts, and once when it clears, instead of on every pass.
type ScanAlertState interface {
	RaiseAlert(ctx context.Context, key string, remind time.Duration) (AlertTransition, error)
	ClearAlert(ctx context.Context, key string) (bool, error)
}

// SetScanAlertState wires where raised scan alerts are remembered; without one every
// pass logs every alert that holds.
func (s *Service) SetScanAlertState(state ScanAlertState) {
	s.alertState = state
}

// alert logs warn when the alert of key is raised, and again as a reminder; it logs
// cleared once when an alert that was raised no longer holds. A state store that
// fails never hides an alert: it is logged as on every pass.
func (s *Service) alert(ctx context.Context, key string, holds bool, warn func(reminder bool), cleared func()) {
	if s.alertState == nil {
		if holds {
			warn(false)
		}
		return
	}
	if !holds {
		if was, err := s.alertState.ClearAlert(ctx, key); err == nil && was {
			cleared()
		}
		return
	}
	transition, err := s.alertState.RaiseAlert(ctx, key, scanAlertReminder)
	switch {
	case err != nil:
		slog.Warn("scan alert state unavailable; the alert is logged on every pass", "key", key, "error", err)
		warn(false)
	case transition == AlertRaised:
		warn(false)
	case transition == AlertReminded:
		warn(true)
	}
}

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

// warnScanHealth runs on every tracker pass, for the chains a scanner records (the
// scan activity): it warns about one whose last completed cycle trailed the head by
// more than its lag threshold or ended longer ago than its stale threshold (the
// scanner stopped, or every cycle fails), and about one with pending blocks. With an
// alert state, an alert is logged when raised, every scanAlertReminder while it
// lasts, and once when it clears. It reads the scan activity, the alert state and the
// pending store only, never a chain, and fails nothing. Without scan activity to read,
// the pending blocks of every registered chain are warned about.
func (s *Service) warnScanHealth(ctx context.Context) {
	scanned := s.warnScanActivity(ctx)
	s.warnPendingBlocks(ctx, scanned)
}

// warnScanActivity alerts on each scanned chain and returns the scanned chains, nil
// when the scan activity could not be read.
func (s *Service) warnScanActivity(ctx context.Context) map[string]bool {
	if s.activity == nil {
		return nil
	}
	activity, err := s.readScanActivity(ctx)
	if err != nil {
		slog.Error("deposit scan activity unreadable; scan lag alerts skipped this pass", "error", err)
		return nil
	}
	now := s.now()
	scanned := map[string]bool{}
	for _, chainID := range sortedChains(activity) {
		if activity[chainID].SignatureMode() {
			scanned[chainID] = true
			s.warnSignatureSync(ctx, chainID, *activity[chainID].Signatures, now)
			continue
		}
		s.clearSignatureAlerts(ctx, chainID)
		scan := activity[chainID].Scan
		if scan == nil {
			continue
		}
		scanned[chainID] = true
		thresholds := s.scanAlertThresholdsFor(ctx, chainID)
		lastScannedAt := scan.At.UTC().Format(time.RFC3339)
		lag, age := scan.LagBlocks(), now.Sub(scan.At)
		s.alert(ctx, "scan-lag:"+chainID, lag > thresholds.LagBlocks, func(reminder bool) {
			slog.Warn(scanLagMessage,
				"chain", chainID, "lag_blocks", lag, "threshold_blocks", thresholds.LagBlocks,
				"head", scan.Head, "checkpoint", scan.Checkpoint, "last_scanned_at", lastScannedAt,
				"reminder", reminder, "remind_every_minutes", int(scanAlertReminder/time.Minute))
		}, func() {
			slog.Info(scanLagClearedMessage, "chain", chainID, "lag_blocks", lag, "threshold_blocks", thresholds.LagBlocks)
		})
		s.alert(ctx, "scan-stale:"+chainID, age > thresholds.StaleAfter, func(reminder bool) {
			slog.Warn(scanStaleMessage,
				"chain", chainID, "minutes_since_scan", int(age/time.Minute), "threshold_minutes", int(thresholds.StaleAfter/time.Minute),
				"last_scanned_at", lastScannedAt, "lag_blocks", lag,
				"reminder", reminder, "remind_every_minutes", int(scanAlertReminder/time.Minute))
		}, func() {
			slog.Info(scanStaleClearedMessage, "chain", chainID, "last_scanned_at", lastScannedAt)
		})
	}
	return scanned
}

// warnSignatureSync alerts on a chain whose deposits the signature sync reads: its
// last completed cycle ended longer ago than its stale threshold plus the sync's
// interval (the sync stopped, or every cycle fails), or left accounts failed (their
// deposits wait for a later cycle). Its block scan records no cycle meanwhile, so
// that scan's alerts are cleared rather than raised forever.
func (s *Service) warnSignatureSync(ctx context.Context, chainID string, sync SignatureSyncRecord, now time.Time) {
	thresholds := s.scanAlertThresholdsFor(ctx, chainID)
	staleAfter := thresholds.StaleAfter + s.SignatureSettings(ctx).Interval
	lastSyncedAt := sync.At.UTC().Format(time.RFC3339)
	age := now.Sub(sync.At)
	s.alert(ctx, "signature-stale:"+chainID, age > staleAfter, func(reminder bool) {
		slog.Warn(signatureStaleMessage,
			"chain", chainID, "minutes_since_sync", int(age/time.Minute), "threshold_minutes", int(staleAfter/time.Minute),
			"last_synced_at", lastSyncedAt, "reminder", reminder, "remind_every_minutes", int(scanAlertReminder/time.Minute))
	}, func() {
		slog.Info(signatureStaleClearedMessage, "chain", chainID, "last_synced_at", lastSyncedAt)
	})
	s.alert(ctx, "signature-failed:"+chainID, sync.FailedAccounts > 0, func(reminder bool) {
		slog.Warn(signatureFailedMessage,
			"chain", chainID, "failed_accounts", sync.FailedAccounts, "accounts", sync.Accounts, "last_synced_at", lastSyncedAt,
			"hint", "the next cycle reads them again from their cursors; deposits:sync-signatures "+chainID+" runs one now",
			"reminder", reminder, "remind_every_minutes", int(scanAlertReminder/time.Minute))
	}, func() {
		slog.Info(signatureFailedClearedMessage, "chain", chainID, "accounts", sync.Accounts)
	})
	quiet := func(bool) {}
	s.alert(ctx, "scan-lag:"+chainID, false, quiet, func() {
		slog.Info(scanLagClearedMessage, "chain", chainID, "reason", "the signature sync reads the chain")
	})
	s.alert(ctx, "scan-stale:"+chainID, false, quiet, func() {
		slog.Info(scanStaleClearedMessage, "chain", chainID, "reason", "the signature sync reads the chain")
	})
}

// clearSignatureAlerts clears the signature sync alerts of a chain the block scan
// reads again.
func (s *Service) clearSignatureAlerts(ctx context.Context, chainID string) {
	quiet := func(bool) {}
	s.alert(ctx, "signature-stale:"+chainID, false, quiet, func() {
		slog.Info(signatureStaleClearedMessage, "chain", chainID, "reason", "the block scan reads the chain")
	})
	s.alert(ctx, "signature-failed:"+chainID, false, quiet, func() {
		slog.Info(signatureFailedClearedMessage, "chain", chainID, "reason", "the block scan reads the chain")
	})
}

// warnPendingBlocks alerts on the pending blocks of the scanned chains (every
// registered chain when scanned is nil).
func (s *Service) warnPendingBlocks(ctx context.Context, scanned map[string]bool) {
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
		if scanned != nil && !scanned[chainID] {
			continue
		}
		count := counts[chainID]
		s.alert(ctx, "pending:"+chainID, count > 0, func(reminder bool) {
			slog.Warn(pendingBlocksMessage, "chain", chainID, "pending_blocks", count,
				"hint", "the scan retries them; scan:deposits "+chainID+" --pending lists them",
				"reminder", reminder, "remind_every_minutes", int(scanAlertReminder/time.Minute))
		}, func() {
			slog.Info(pendingBlocksClearedMessage, "chain", chainID)
		})
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
