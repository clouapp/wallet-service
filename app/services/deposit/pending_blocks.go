package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
)

var errNoPendingStore = errors.New("no pending deposit store configured")

// ReprocessResult counts what one pass over a chain's pending blocks did.
// The scan loop's pass over pending blocks runs after each scan of the chain: it
// retries at most pendingPassMaxBlocks due blocks, oldest first, and starts no new
// block once pendingPassBudget is spent, so a pile of pending blocks (an outage of
// the endpoints that confirm skipped slots) never holds the scan of new blocks back.
// The rest wait for the next pass. An operator's forced pass has no limit.
const (
	pendingPassMaxBlocks = 25
	pendingPassBudget    = 5 * time.Second
)

type ReprocessResult struct {
	Due          int
	Resolved     int
	StillPending int
	// Deferred is the due blocks left for the next pass by its limits.
	Deferred int
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

// deferPending hands blocks to the pending retry, due now: a reorganization rescan
// that ran out of time leaves the rest of the new fork to it (reason). A block that
// already has an entry keeps it. It returns how many blocks it handed over.
func (s *Service) deferPending(ctx context.Context, chainID string, blocks []uint64, reason string) (int, error) {
	if len(blocks) == 0 {
		return 0, nil
	}
	if s.pending == nil {
		return 0, errNoPendingStore
	}
	entries, err := s.pending.List(ctx, chainID)
	if err != nil {
		return 0, fmt.Errorf("list pending %s blocks: %w", chainID, err)
	}
	kept := make(map[uint64]bool, len(entries))
	for _, entry := range entries {
		kept[entry.Block] = true
	}
	now := s.now()
	handed := 0
	for _, block := range blocks {
		if kept[block] {
			continue
		}
		entry := pending.Entry{Chain: chainID, Block: block, ErrorClass: pending.ClassUnknown, LastError: reason,
			Attempts: 1, FirstFailedAt: now, LastFailedAt: now, NextRetryAt: now}
		if err := s.pending.Put(ctx, entry); err != nil {
			return handed, fmt.Errorf("keep %s block %d pending: %w", chainID, block, err)
		}
		handed++
	}
	return handed, nil
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
	started := s.now()
	for i, entry := range due {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !force && (i == pendingPassMaxBlocks || s.now().Sub(started) >= pendingPassBudget) {
			result.Deferred = len(due) - i
			slog.Info("pending deposit blocks left for the next pass", "chain", chainID, "deferred", result.Deferred)
			break
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

// ReprocessDuePending is the scan loop's pass over pending blocks whose backoff
// elapsed. It also runs for a chain whose deposits the signature sync reads: the sync
// drops the pending blocks its histories cover (resolveCoveredPending), and the few
// older ones are only ever recorded by this retry.
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

// pendingHealthCacheFor is how long the health check reuses its pending counts.
const pendingHealthCacheFor = 15 * time.Second

// PendingHealth is the pending-block counts the health check reports. Err
// is set when the store, or a chain's list, could not be read; Counts keeps
// what could.
type PendingHealth struct {
	Counts map[string]int
	Total  int
	Err    error
}

var errPendingHealthBusy = errors.New("pending deposit blocks are being counted")

// PendingHealth reads PendingCounts and sums them. It never fails: pending blocks are
// recovered by the reprocessor, so they are reported, not treated as the API being
// down. The counts are reused for pendingHealthCacheFor and read one at a time,
// bounded by healthReadBudget (healthSnapshot); a reading that fails is reported as
// it is, with what it could count, and answers the checks of the next
// healthFailureBackoff.
func (s *Service) PendingHealth(ctx context.Context) PendingHealth {
	return s.pendingCache.get(ctx, s.now(), pendingHealthCacheFor, healthRead[PendingHealth]{
		Read: func(ctx context.Context) (PendingHealth, error) {
			counts, err := s.PendingCounts(ctx)
			health := PendingHealth{Counts: counts, Err: err}
			for _, count := range counts {
				health.Total += count
			}
			return health, err
		},
		Unavailable: func(err error) PendingHealth { return PendingHealth{Err: err} },
		KeepFailure: true,
		Failure:     "deposit scanner pending counts",
		Busy:        errPendingHealthBusy,
	})
}

// HealthReadings are the readings GET /health reports.
type HealthReadings struct {
	Pending  PendingHealth
	Stale    StaleOutboundHealth
	Activity ScanActivityHealth
}

// Health takes the readings of GET /health at once, so a store that does not answer
// costs the check one reading budget, not one per reading.
func (s *Service) Health(ctx context.Context) HealthReadings {
	var readings HealthReadings
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		readings.Pending = s.PendingHealth(ctx)
	}()
	go func() {
		defer wg.Done()
		readings.Stale = s.StaleOutboundHealth(ctx)
	}()
	go func() {
		defer wg.Done()
		readings.Activity = s.ScanActivityHealth(ctx)
	}()
	wg.Wait()
	return readings
}
