package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/pkg/types"
)

var (
	// ErrSignaturesUnsupported means the chain's adapter cannot read deposits account
	// by account (it is no chain.SignatureReader).
	ErrSignaturesUnsupported = errors.New("deposit: the chain's adapter cannot read deposits by account signatures")
	// ErrSignatureMode means the chain's deposits are read by the signature sync, so
	// its block scan does not run.
	ErrSignatureMode = errors.New("deposit: the chain is in signature mode, its block scan is paused")
	// ErrScanLockLost means a signature sync cycle lost the chain's scan lock (another
	// process took it, or the cache could not be read or written): it stopped, its
	// progress saved, without confirming or dropping anything.
	ErrScanLockLost = errors.New("deposit: the signature sync lost the chain's scan lock")

	errNoSignatureCursorStore = errors.New("deposit: no signature cursor store configured")
	errSignatureNoTime        = errors.New("deposit: too little time left before the deadline for a signature sync cycle")
)

const (
	// signatureCycleBudget is how long one cycle reads and records signatures: an
	// account started after it waits for the next cycle, one in progress stops at
	// its next signature, its cursor saved.
	signatureCycleBudget = 8 * time.Minute
	// signatureAccountBudget is how long one account reads and records in a cycle.
	signatureAccountBudget = 3 * time.Minute
	// signatureDeadlineMargin is how long before its context's deadline (the
	// deposit_scanner Lambda's) a cycle's budget ends: an account in progress stops at
	// its next signature, which leaves a read in flight, the confirmation step and the
	// record their time. A context with less than that left runs no cycle.
	signatureDeadlineMargin = 20 * time.Second
	// signatureSaveReserve is how long before that deadline the cycle's context ends,
	// cutting whatever read is still in flight: what is left is the final cursor saves'
	// (signatureCursorSaveTimeout each, run at once), so they land before the deadline.
	signatureSaveReserve = signatureCursorSaveTimeout + time.Second
)

// SignatureCursorStore keeps how far the signature sync of each account of a chain
// got; Advance never moves a cursor back.
type SignatureCursorStore interface {
	FindByChain(ctx context.Context, chainID string) ([]models.DepositSignatureCursor, error)
	Advance(ctx context.Context, cursor models.DepositSignatureCursor) error
}

// SignatureSyncSummary is what one SyncSignatures cycle did: the accounts it read
// (an owner whose accounts could not be listed counts as one, failed), the
// signatures it read, the deposits it recorded, the accounts that failed (their
// cursor stays before the failure, so the next cycle reads them again), those not
// read down to their floor yet, with a gap above their cursor still read, or left
// behind by the cycle's budget (backfilling: the next cycle goes on), the registered
// mints the node does not hold as token mints, and the pending blocks it found
// covered. CoveredHead is the slot every account was read up to when this cycle read
// them all (0 otherwise: a failed, unlisted or backfilling account, or a skipped
// mint, whose accounts were not read); Head the slot it confirmed at. Skipped is a
// cycle that found another scan holding the chain and did nothing.
type SignatureSyncSummary struct {
	Accounts            int
	NewSignatures       int
	Deposits            int
	FailedAccounts      int
	BackfillingAccounts int
	SkippedMints        []string
	ResolvedPending     int
	CoveredHead         uint64
	Head                uint64
	Skipped             bool
}

// SyncSignatures records the deposits of chainID read account by account: for every
// watched address, its accounts (chain.SignatureReader.SignatureAccounts), the new
// signatures of each account above its cursor (oldest first), and, until the account
// is read down to its floor, its older history (newest first, at most a few pages a
// cycle, so a long history is read over several cycles). The floor of an account
// first seen is the chain's scan checkpoint lowered to its lowest pending block,
// which recovers what the block scan left; a chain with neither reads at most
// MaxPages of each history. The credits of each successful transaction are recorded
// through the scanner's path (recordTransfers: the watched-address check, internal
// transfers left out, depositrecord's one row per transfer), so a deposit the block
// scan or the provider push also saw is one row. An account's cursor moves past a
// signature only once its transfers are all recorded, and is saved as it goes, with a
// context of its own, so a cycle cut short (a deadline, a shutdown) keeps what it
// recorded. When every account was read up to now, the pending blocks the histories
// cover are dropped and the slot they were read up to is recorded (the block scan
// resumes after it). The chain's confirmation step then runs at the head. The cycle
// holds the chain's scan lock, renewed while it runs; another scan holding it skips
// the cycle (Skipped, no error), and a lock lost on the way stops it
// (ErrScanLockLost). The balances are refreshed once the lock is released. Under a
// context with a deadline the cycle's budget ends signatureDeadlineMargin before it
// and its reads are cut signatureSaveReserve before it, so the cursors are saved in
// time; with less than the margin left it runs no cycle.
func (s *Service) SyncSignatures(ctx context.Context, chainID string) (SignatureSyncSummary, error) {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return SignatureSyncSummary{}, err
	}
	reader, ok := adapter.(chain.SignatureReader)
	if !ok {
		return SignatureSyncSummary{}, fmt.Errorf("%w: %s", ErrSignaturesUnsupported, chainID)
	}
	if s.signatureCursors == nil {
		return SignatureSyncSummary{}, errNoSignatureCursorStore
	}
	settings := s.SignatureSettings(ctx)
	// The time left is the wall clock's; the budget is then counted on the service's.
	budget := signatureCycleBudget
	deadline, bounded := ctx.Deadline()
	if bounded {
		left := time.Until(deadline)
		if left <= signatureDeadlineMargin {
			return SignatureSyncSummary{}, fmt.Errorf("deposit signature sync of %s: %w (%s left)", chainID, errSignatureNoTime, left.Round(time.Second))
		}
		budget = min(budget, left-signatureDeadlineMargin)
	}
	hold, err := s.holdChain(chainID)
	if errors.Is(err, ErrScanInProgress) {
		slog.Info("deposit signature sync skipped, another scan holds the chain", "chain", chainID)
		return SignatureSyncSummary{Skipped: true}, nil
	}
	if err != nil {
		return SignatureSyncSummary{}, err
	}
	var cycleCtx context.Context
	var stop context.CancelFunc
	if bounded {
		cycleCtx, stop = context.WithDeadline(ctx, deadline.Add(-signatureSaveReserve))
	} else {
		cycleCtx, stop = context.WithCancel(ctx)
	}
	go func() {
		select {
		case <-hold.Lost():
			stop()
		case <-cycleCtx.Done():
		}
	}()
	confirmed := newWalletSet()
	summary, err := s.syncSignatures(cycleCtx, chainID, adapter, reader, settings, hold, s.now().Add(budget), confirmed)
	stop()
	hold.Release()
	s.refreshBalances(ctx, confirmed.ids)
	if err != nil {
		return summary, err
	}
	s.recordSignatureSync(ctx, chainID, summary)
	slog.Info("deposit signature sync complete", "chain", chainID, "accounts", summary.Accounts,
		"new_signatures", summary.NewSignatures, "deposits", summary.Deposits, "failed_accounts", summary.FailedAccounts,
		"backfilling_accounts", summary.BackfillingAccounts, "resolved_pending", summary.ResolvedPending,
		"covered_head", summary.CoveredHead)
	return summary, nil
}

// SyncSignaturesWhenDue runs one SyncSignatures cycle of chainID when
// deposit_signatures lists it and its interval elapsed since the last completed cycle
// (the scan activity's record, so it holds across processes and Lambda invocations);
// ran is false otherwise. A record that cannot be read runs the cycle.
func (s *Service) SyncSignaturesWhenDue(ctx context.Context, chainID string) (summary SignatureSyncSummary, ran bool, err error) {
	settings := s.SignatureSettings(ctx)
	if !settings.Enabled(chainID) {
		return SignatureSyncSummary{}, false, nil
	}
	activity, readErr := s.chainActivity(ctx, chainID)
	if readErr != nil {
		slog.Warn("deposit signature sync: last cycle unread, syncing now", "chain", chainID, "error", readErr)
	} else if last := activity.Signatures; last != nil && s.now().Sub(last.At) < settings.Interval {
		return SignatureSyncSummary{}, false, nil
	}
	summary, err = s.SyncSignatures(ctx, chainID)
	return summary, true, err
}

// syncSignatures is one cycle under the chain's scan lock, whose accounts start and
// read until deadline; the wallets whose movements it confirmed are added to
// confirmed.
func (s *Service) syncSignatures(ctx context.Context, chainID string, adapter types.Chain, reader chain.SignatureReader, settings SignatureSyncSettings, hold *cacheguard.Hold, deadline time.Time, confirmed *walletSet) (SignatureSyncSummary, error) {
	var summary SignatureSyncSummary
	// Read before the addresses: an address created later received nothing up to it.
	listedAt, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		return summary, fmt.Errorf("deposit signature sync of %s: head: %w", chainID, err)
	}
	owners, err := s.addressRepo.PluckActiveAddresses(ctx, chainID)
	if err != nil {
		return summary, fmt.Errorf("deposit signature sync of %s: %w", chainID, err)
	}
	floor, err := s.signatureFloor(ctx, chainID)
	if err != nil {
		return summary, fmt.Errorf("deposit signature sync of %s: %w", chainID, err)
	}
	rows, err := s.signatureCursors.FindByChain(ctx, chainID)
	if err != nil {
		return summary, fmt.Errorf("deposit signature sync of %s: %w", chainID, err)
	}
	cursors := make(map[string]models.DepositSignatureCursor, len(rows))
	for _, row := range rows {
		cursors[row.Account] = row
	}

	cycle := &signatureCycle{svc: s, chainID: chainID, adapter: adapter, reader: reader, settings: settings, floor: floor,
		hold: hold, deadline: deadline,
		transactions: map[string]*signatureOutcome{}, hashes: map[uint64]*slotHash{}}
	accounts, unlisted, skippedMints := cycle.accounts(ctx, owners)
	summary.Accounts = len(accounts) + unlisted
	summary.FailedAccounts = unlisted
	summary.SkippedMints = skippedMints
	if len(skippedMints) > 0 {
		slog.Error("deposit signature sync: registered tokens the chain's node does not hold as token mints are not read; fix the token registry",
			"chain", chainID, "mints", skippedMints)
	}

	results := make([]accountSync, len(accounts))
	forEachConcurrently(ctx, settings.Concurrency, len(accounts), func(i int) {
		var cursor *models.DepositSignatureCursor
		if row, ok := cursors[accounts[i]]; ok {
			cursor = &row
		}
		results[i] = cycle.syncAccount(ctx, accounts[i], cursor)
	})
	// A skipped mint's accounts were not listed, so they were not read either.
	everyAccountRead := unlisted == 0 && len(accounts) > 0 && len(skippedMints) == 0
	coveredHead, coveredFrom := listedAt, uint64(0)
	for i, result := range results {
		summary.NewSignatures += result.signatures
		summary.Deposits += result.deposits
		switch {
		case result.err != nil:
			summary.FailedAccounts++
			everyAccountRead = false
			slog.Error("deposit signature sync of an account failed; its cursor stays before the failure and the next cycle reads it again",
				"chain", chainID, "account", accounts[i], "error", result.err, "provider_cause", chain.CauseText(result.err))
		case result.coveredTo == 0:
			summary.BackfillingAccounts++
			everyAccountRead = false
		default:
			coveredHead = min(coveredHead, result.coveredTo)
			coveredFrom = max(coveredFrom, result.syncedFrom)
		}
	}
	if !hold.Held() {
		return summary, fmt.Errorf("deposit signature sync of %s: %w", chainID, ErrScanLockLost)
	}
	if err := ctx.Err(); err != nil {
		return summary, fmt.Errorf("deposit signature sync of %s interrupted: %w", chainID, err)
	}
	if everyAccountRead {
		summary.CoveredHead = coveredHead
		resolved, err := s.resolveCoveredPending(ctx, chainID, coveredFrom)
		summary.ResolvedPending = resolved
		if err != nil {
			slog.Warn("pending deposit blocks the signature sync covers were not all dropped; the next cycle drops them", "chain", chainID, "error", err)
		}
	}
	summary.Head = s.confirmSignatureDeposits(ctx, chainID, adapter, confirmed)
	return summary, nil
}

// signatureFloor is the slot the history of an account without a cursor is read
// back to (ok false: none, the history is bounded by MaxPages).
type signatureFloor struct {
	slot uint64
	ok   bool
}

// signatureFloor is the chain's scan checkpoint, lowered to its lowest pending
// block: the block scan recorded the deposits up to the checkpoint except those of
// its pending blocks. A checkpoint that cannot be read, or a pending store read that
// missed a backend (pending.ListComplete), fails the cycle: a floor set too high
// would skip deposits for good.
func (s *Service) signatureFloor(ctx context.Context, chainID string) (signatureFloor, error) {
	var floor signatureFloor
	checkpoint, found, err := s.loadCheckpoint(ctx, chainID)
	if err != nil {
		return floor, fmt.Errorf("signature floor: %w", err)
	}
	if found {
		floor = signatureFloor{slot: checkpoint, ok: true}
	}
	if s.pending == nil {
		return floor, nil
	}
	entries, err := pending.ListComplete(ctx, s.pending, chainID)
	if err != nil {
		return floor, fmt.Errorf("signature floor: list pending %s blocks: %w", chainID, err)
	}
	for _, entry := range entries {
		if !floor.ok || entry.Block < floor.slot {
			floor = signatureFloor{slot: entry.Block, ok: true}
		}
	}
	return floor, nil
}

// resolveCoveredPending drops the chain's pending blocks at or above coveredFrom:
// every account of the chain has just been read from a slot no higher than that up
// to now, so the deposits those blocks hold are recorded. Older pending blocks stay,
// for the pending retry.
func (s *Service) resolveCoveredPending(ctx context.Context, chainID string, coveredFrom uint64) (int, error) {
	if s.pending == nil {
		return 0, nil
	}
	entries, err := s.pending.List(ctx, chainID)
	if err != nil {
		return 0, fmt.Errorf("list pending %s blocks: %w", chainID, err)
	}
	resolved, older := 0, 0
	for _, entry := range entries {
		if entry.Block < coveredFrom {
			older++
			continue
		}
		if err := s.pending.Delete(ctx, chainID, entry.Block); err != nil {
			return resolved, fmt.Errorf("drop pending %s block %d: %w", chainID, entry.Block, err)
		}
		resolved++
		slog.Info("pending deposit block covered by the signature sync", "chain", chainID, "block", entry.Block, "attempts", entry.Attempts)
	}
	if older > 0 {
		slog.Warn("pending deposit blocks older than the signature sync's histories stay pending; the pending retry reads them", "chain", chainID, "blocks", older, "covered_from_slot", coveredFrom)
	}
	return resolved, nil
}

// confirmSignatureDeposits runs the chain's confirmation step at the head, as the
// block scan does after each cycle: a finalized Solana slot is final, so a deposit
// the sync recorded confirms in the same cycle. It returns the head it confirmed at,
// 0 when the head could not be read (the confirmations wait for the next cycle).
func (s *Service) confirmSignatureDeposits(ctx context.Context, chainID string, adapter types.Chain, confirmed *walletSet) uint64 {
	head, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		slog.Error("deposit signature sync: head not read, confirmations left to the next cycle", "chain", chainID, "error", err)
		return 0
	}
	rows, err := s.txRepo.FindPendingByChain(ctx, chainID)
	if err != nil {
		slog.Error("deposit signature sync: pending transactions not read, confirmations left to the next cycle", "chain", chainID, "error", err)
		return head
	}
	if err := s.confirmPending(ctx, adapter, head, rows, confirmed); err != nil {
		slog.Error("update confirmations failed", "chain", chainID, "error", err)
	}
	return head
}

// signatureCycle is one SyncSignatures cycle of a chain. A transaction is read and
// recorded once per cycle, however many of its accounts list it (an owner and its
// token account often do), and a slot's hash is read once.
type signatureCycle struct {
	svc      *Service
	chainID  string
	adapter  types.Chain
	reader   chain.SignatureReader
	settings SignatureSyncSettings
	floor    signatureFloor
	hold     *cacheguard.Hold
	deadline time.Time

	mu           sync.Mutex
	transactions map[string]*signatureOutcome
	hashes       map[uint64]*slotHash
}

// signatureOutcome is a transaction's outcome in this cycle; done is closed once it
// is known.
type signatureOutcome struct {
	done     chan struct{}
	deposits int
	err      error
}

type slotHash struct {
	done chan struct{}
	hash string
	err  error
}

// accounts lists the accounts of every owner, without repeats, how many owners
// could not be listed (each skipped this cycle and counted as a failed account), and
// the registered mints the node does not hold as token mints.
func (c *signatureCycle) accounts(ctx context.Context, owners []string) ([]string, int, []string) {
	listed := make([]chain.SignatureAccounts, len(owners))
	failures := make([]error, len(owners))
	forEachConcurrently(ctx, c.settings.Concurrency, len(owners), func(i int) {
		listed[i], failures[i] = c.reader.SignatureAccounts(ctx, owners[i])
	})
	var accounts, skipped []string
	seen := map[string]bool{}
	unlisted := 0
	for i, owner := range owners {
		if failures[i] != nil {
			unlisted++
			slog.Error("deposit signature sync: the accounts of an address were not listed; it is read on the next cycle",
				"chain", c.chainID, "address", owner, "error", failures[i], "provider_cause", chain.CauseText(failures[i]))
			continue
		}
		for _, mint := range listed[i].SkippedMints {
			if !slices.Contains(skipped, mint) {
				skipped = append(skipped, mint)
			}
		}
		for _, account := range listed[i].Accounts {
			if !seen[account] {
				seen[account] = true
				accounts = append(accounts, account)
			}
		}
	}
	slices.Sort(skipped)
	return accounts, unlisted, skipped
}

// record records one successful transaction once per cycle and returns the deposits
// it created to the first account that read it (0 to the others, which share its
// outcome).
func (c *signatureCycle) record(ctx context.Context, signature chain.AccountSignature) (int, error) {
	c.mu.Lock()
	outcome, seen := c.transactions[signature.Signature]
	if !seen {
		outcome = &signatureOutcome{done: make(chan struct{})}
		c.transactions[signature.Signature] = outcome
	}
	c.mu.Unlock()
	if seen {
		select {
		case <-outcome.done:
			return 0, outcome.err
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	outcome.deposits, outcome.err = c.recordTransaction(ctx, signature)
	close(outcome.done)
	return outcome.deposits, outcome.err
}

// recordTransaction reads the credits of one transaction and records those to
// watched addresses, with the hash of their slot, through the scanner's path.
func (c *signatureCycle) recordTransaction(ctx context.Context, signature chain.AccountSignature) (int, error) {
	read, err := c.reader.TransactionTransfers(ctx, signature.Signature)
	if err != nil {
		return 0, classify(pending.ClassRPC, fmt.Errorf("read transaction %s: %w", signature.Signature, err))
	}
	var watched []types.DetectedTransfer
	for _, transfer := range read.Transfers {
		isWatched, watchErr := c.svc.isWatchedAddress(ctx, c.chainID, transfer.To)
		if watchErr != nil {
			return 0, classify(pending.ClassDatabase, fmt.Errorf("transaction %s: %w", signature.Signature, watchErr))
		}
		if isWatched {
			watched = append(watched, transfer)
		}
	}
	if len(watched) == 0 {
		return 0, nil
	}
	hash, err := c.blockHash(ctx, read.Slot)
	if err != nil {
		return 0, classify(pending.ClassRPC, fmt.Errorf("transaction %s: %w", signature.Signature, err))
	}
	for i := range watched {
		watched[i].BlockHash = hash
	}
	recorded, _, err := c.svc.recordTransfers(ctx, c.chainID, c.adapter, watched)
	if err != nil {
		return recorded, fmt.Errorf("record transaction %s: %w", signature.Signature, err)
	}
	return recorded, nil
}

// blockHash is the hash of slot, read once per cycle (a failed read too).
func (c *signatureCycle) blockHash(ctx context.Context, slot uint64) (string, error) {
	c.mu.Lock()
	entry, seen := c.hashes[slot]
	if !seen {
		entry = &slotHash{done: make(chan struct{})}
		c.hashes[slot] = entry
	}
	c.mu.Unlock()
	if seen {
		select {
		case <-entry.done:
			return entry.hash, entry.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	entry.hash, entry.err = c.reader.BlockHashAt(ctx, slot)
	if entry.err != nil {
		entry.err = fmt.Errorf("the hash of slot %d was not read: %w", slot, entry.err)
	}
	close(entry.done)
	return entry.hash, entry.err
}

// forEachConcurrently runs fn(0..n-1), at most limit at once, and returns when they
// all returned; once ctx ends no new call starts.
func forEachConcurrently(ctx context.Context, limit, n int, fn func(i int)) {
	if limit < 1 {
		limit = 1
	}
	slots := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			fn(i)
		})
	}
	wg.Wait()
}

// confirmWhileSigning runs the confirmation step of a chain whose block scan is paused
// for the signature sync, at the head, under the chain's scan lock, on the scan's
// cadence: a deposit the provider push recorded (the fast path) confirms within a scan
// interval instead of waiting for the next sync cycle. While a sync cycle holds the
// lock it is skipped (the cycle confirms at its end); with no pending transaction the
// head is not read. The balances of the wallets it confirmed are refreshed once the
// lock is released.
func (s *Service) confirmWhileSigning(ctx context.Context, chainID string, adapter types.Chain) {
	release, err := s.lockChain(chainID)
	if errors.Is(err, ErrScanInProgress) {
		return
	}
	if err != nil {
		slog.Warn("confirmations of a chain in signature mode left to the next cycle", "chain", chainID, "error", err)
		return
	}
	confirmed := newWalletSet()
	func() {
		defer release()
		s.confirmUnderLock(ctx, chainID, adapter, confirmed)
	}()
	s.refreshBalances(ctx, confirmed.ids)
}

// confirmUnderLock is confirmWhileSigning's step under the lock: the wallets whose
// movements it confirmed are added to confirmed, refreshed once the lock is released.
func (s *Service) confirmUnderLock(ctx context.Context, chainID string, adapter types.Chain, confirmed *walletSet) {
	rows, err := s.txRepo.FindPendingByChain(ctx, chainID)
	if err != nil {
		slog.Warn("confirmations of a chain in signature mode left to the next cycle", "chain", chainID, "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	head, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		slog.Warn("confirmations of a chain in signature mode left to the next cycle", "chain", chainID, "error", err)
		return
	}
	if err := s.confirmPending(ctx, adapter, head, rows, confirmed); err != nil {
		slog.Error("update confirmations failed", "chain", chainID, "error", err)
	}
}

// resumeAfterSignatureSync moves the checkpoint of a chain the signature sync read,
// whose block scan resumes, up to the slot the sync's last cycle read every account
// up to (sync.CoveredHead), when that cycle read them all (ReadEveryAccount) and the
// slot is past the checkpoint. That slot is safe to resume after: a cycle records it
// only when every account of every address listed at its start was read, with proof,
// from its floor up to a head at least signatureIndexMargin above it, and an address
// created later had received nothing up to the slot read before the addresses were
// listed; the pending blocks under the histories' floors stay pending. A covered head
// carried from an earlier cycle is never resumed after: an account listed since (a
// token registered later, a mint the node held again) may hold older deposits.
// Otherwise the scan resumes from its checkpoint.
func (s *Service) resumeAfterSignatureSync(ctx context.Context, chainID string, sync *SignatureSyncRecord) error {
	if sync == nil || !sync.ReadEveryAccount || sync.CoveredHead == 0 {
		return nil
	}
	checkpoint, found, err := s.loadCheckpoint(ctx, chainID)
	if err != nil {
		return fmt.Errorf("deposit scan skipped: %w", err)
	}
	if found && checkpoint >= sync.CoveredHead {
		return nil
	}
	if err := s.saveCheckpoint(ctx, chainID, sync.CoveredHead); err != nil {
		return fmt.Errorf("deposit scan resume: %w", err)
	}
	slog.Info("deposit block scan resumes after the slot the signature sync read every account up to",
		"chain", chainID, "checkpoint", checkpoint, "covered_head", sync.CoveredHead)
	return nil
}

// blockScanPaused reports whether chainID's block scan pauses because the signature
// sync reads its deposits, logging when that changes. A chain the settings enable
// whose adapter cannot read signatures keeps its block scan (a warning says so), so
// a setting can never leave a chain without a detector.
func (s *Service) blockScanPaused(ctx context.Context, chainID string, adapter types.Chain) bool {
	enabled := s.SignatureSettings(ctx).Enabled(chainID)
	_, readable := adapter.(chain.SignatureReader)
	paused := enabled && readable
	previous, seen := s.signatureModes.Swap(chainID, paused)
	if seen && previous.(bool) == paused {
		return paused
	}
	switch {
	case paused:
		slog.Info("deposit block scan paused: the signature sync reads the chain's deposits", "chain", chainID)
	case enabled:
		slog.Warn("deposit signature sync enabled for a chain whose adapter cannot read signatures; its block scan keeps running", "chain", chainID)
	case seen:
		slog.Info("deposit block scan resumed: the signature sync is off for the chain", "chain", chainID)
	}
	return paused
}
