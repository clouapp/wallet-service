package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"

	"github.com/macrowallets/waas/app/models"
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

	errNoSignatureCursorStore = errors.New("deposit: no signature cursor store configured")
)

// signatureHistoryPageCeiling bounds, in pages of 1000 signatures, the history of an
// account read back to its cursor or to the floor. A history longer than that is
// never cut there: older signatures could hold deposits the sync would skip for
// good, so the account fails instead and keeps its cursor.
const signatureHistoryPageCeiling = 100

// SignatureCursorStore keeps where the signature sync of each account of a chain
// resumes; Advance never moves a cursor back.
type SignatureCursorStore interface {
	FindByChain(ctx context.Context, chainID string) ([]models.DepositSignatureCursor, error)
	Advance(ctx context.Context, cursor models.DepositSignatureCursor) error
}

// SignatureSyncSummary is what one SyncSignatures cycle did: the accounts it read
// (an owner whose accounts could not be listed counts as one, failed), the
// signatures newer than their cursors, the deposits it recorded, the accounts that
// failed (their cursor stays before the failure, so the next cycle reads them
// again) and the pending blocks it found covered. Skipped is a cycle that found
// another scan holding the chain and did nothing.
type SignatureSyncSummary struct {
	Accounts        int
	NewSignatures   int
	Deposits        int
	FailedAccounts  int
	ResolvedPending int
	Skipped         bool
}

// SyncSignatures records the deposits of chainID read account by account: for
// every watched address, its accounts (chain.SignatureReader.SignatureAccounts), the
// signatures of each account since its cursor, oldest first, and the credits of
// each successful transaction, recorded through the scanner's path
// (recordTransfers: the watched-address check, internal transfers left out,
// depositrecord's one row per transfer), so a deposit the block scan or the provider
// push also saw is one row. An account's cursor moves past a signature only once its
// transfers are all recorded. An account without a cursor is read back to the
// floor: the chain's scan checkpoint, or its lowest pending block when lower, which
// recovers what the block scan left; a chain without either reads at most MaxPages
// of each history. When every account was read, the pending blocks the histories
// cover are dropped. The chain's confirmation step then runs at the head. The cycle
// holds the chain's scan lock, renewed while it runs (another scan holding it skips
// the cycle: Skipped, no error); the balances are refreshed after it is released.
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
	release, err := s.holdChain(chainID)
	if errors.Is(err, ErrScanInProgress) {
		slog.Info("deposit signature sync skipped, another scan holds the chain", "chain", chainID)
		return SignatureSyncSummary{Skipped: true}, nil
	}
	if err != nil {
		return SignatureSyncSummary{}, err
	}
	confirmed := newWalletSet()
	summary, err := s.syncSignatures(ctx, chainID, adapter, reader, settings, confirmed)
	release()
	s.refreshBalances(ctx, confirmed.ids)
	if err != nil {
		return summary, err
	}
	s.recordSignatureSync(ctx, chainID, summary)
	slog.Info("deposit signature sync complete", "chain", chainID, "accounts", summary.Accounts,
		"new_signatures", summary.NewSignatures, "deposits", summary.Deposits,
		"failed_accounts", summary.FailedAccounts, "resolved_pending", summary.ResolvedPending)
	return summary, nil
}

// syncSignatures is one cycle under the chain's scan lock; the wallets whose
// movements it confirmed are added to confirmed.
func (s *Service) syncSignatures(ctx context.Context, chainID string, adapter types.Chain, reader chain.SignatureReader, settings SignatureSyncSettings, confirmed *walletSet) (SignatureSyncSummary, error) {
	var summary SignatureSyncSummary
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
		transactions: map[string]*signatureOutcome{}, hashes: map[uint64]*slotHash{}}
	accounts, unlisted := cycle.accounts(ctx, owners)
	summary.Accounts = len(accounts) + unlisted
	summary.FailedAccounts = unlisted

	results := make([]accountSync, len(accounts))
	forEachConcurrently(ctx, settings.Concurrency, len(accounts), func(i int) {
		var cursor *models.DepositSignatureCursor
		if row, ok := cursors[accounts[i]]; ok {
			cursor = &row
		}
		results[i] = cycle.syncAccount(ctx, accounts[i], cursor)
	})
	var coveredFrom uint64
	for i, result := range results {
		summary.NewSignatures += result.signatures
		summary.Deposits += result.deposits
		if result.err != nil {
			summary.FailedAccounts++
			slog.Error("deposit signature sync of an account failed; its cursor stays before the failure and the next cycle reads it again",
				"chain", chainID, "account", accounts[i], "error", result.err, "provider_cause", chain.CauseText(result.err))
			continue
		}
		coveredFrom = max(coveredFrom, result.syncedFrom)
	}
	if err := ctx.Err(); err != nil {
		return summary, fmt.Errorf("deposit signature sync of %s interrupted: %w", chainID, err)
	}
	if summary.FailedAccounts == 0 && len(accounts) > 0 {
		resolved, err := s.resolveCoveredPending(ctx, chainID, coveredFrom)
		summary.ResolvedPending = resolved
		if err != nil {
			slog.Warn("pending deposit blocks the signature sync covers were not all dropped; the next cycle drops them", "chain", chainID, "error", err)
		}
	}
	s.confirmSignatureDeposits(ctx, chainID, adapter, confirmed)
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
// its pending blocks. A checkpoint or a pending store that cannot be read fails the
// cycle, since a floor set too high would skip deposits for good.
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
	entries, err := s.pending.List(ctx, chainID)
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
// to now, so the deposits those blocks hold are recorded. Older pending blocks stay.
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
		slog.Warn("pending deposit blocks older than the signature sync's histories stay pending", "chain", chainID, "blocks", older, "covered_from_slot", coveredFrom)
	}
	return resolved, nil
}

// confirmSignatureDeposits runs the chain's confirmation step at the head, as the
// block scan does after each cycle: a finalized Solana slot is final, so a deposit
// the sync recorded confirms in the same cycle. A head or a pending list that cannot
// be read leaves the confirmations to the next cycle.
func (s *Service) confirmSignatureDeposits(ctx context.Context, chainID string, adapter types.Chain, confirmed *walletSet) {
	head, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		slog.Error("deposit signature sync: head not read, confirmations left to the next cycle", "chain", chainID, "error", err)
		return
	}
	rows, err := s.txRepo.FindPendingByChain(ctx, chainID)
	if err != nil {
		slog.Error("deposit signature sync: pending transactions not read, confirmations left to the next cycle", "chain", chainID, "error", err)
		return
	}
	if err := s.confirmPending(ctx, adapter, head, rows, confirmed); err != nil {
		slog.Error("update confirmations failed", "chain", chainID, "error", err)
	}
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

// accounts lists the accounts of every owner, without repeats, and how many owners
// could not be listed (each skipped this cycle and counted as a failed account).
func (c *signatureCycle) accounts(ctx context.Context, owners []string) ([]string, int) {
	listed := make([][]string, len(owners))
	failures := make([]error, len(owners))
	forEachConcurrently(ctx, c.settings.Concurrency, len(owners), func(i int) {
		listed[i], failures[i] = c.reader.SignatureAccounts(ctx, owners[i])
	})
	var accounts []string
	seen := map[string]bool{}
	unlisted := 0
	for i, owner := range owners {
		if failures[i] != nil {
			unlisted++
			slog.Error("deposit signature sync: the accounts of an address were not listed; it is read on the next cycle",
				"chain", c.chainID, "address", owner, "error", failures[i], "provider_cause", chain.CauseText(failures[i]))
			continue
		}
		for _, account := range listed[i] {
			if !seen[account] {
				seen[account] = true
				accounts = append(accounts, account)
			}
		}
	}
	return accounts, unlisted
}

// accountSync is what reading one account did: the new signatures it read, the
// deposits it recorded first in this cycle, the slot its history covers from, and
// why it stopped early.
type accountSync struct {
	signatures int
	deposits   int
	syncedFrom uint64
	err        error
}

// syncAccount reads account's signatures since its cursor (or since the floor, or at
// most MaxPages of its history), oldest first, records each successful transaction,
// and moves the cursor past the last signature whose transfers were all recorded. A
// failed signature stops the account there.
func (c *signatureCycle) syncAccount(ctx context.Context, account string, cursor *models.DepositSignatureCursor) accountSync {
	query := chain.SignatureQuery{MaxPages: signatureHistoryPageCeiling}
	var result accountSync
	var newest chain.AccountSignature
	switch {
	case cursor != nil:
		query.Until, query.FloorSlot, query.HasFloor = cursor.Signature, storedSlot(cursor.Slot), true
		newest = chain.AccountSignature{Signature: cursor.Signature, Slot: storedSlot(cursor.Slot)}
		result.syncedFrom = storedSlot(cursor.SyncedFromSlot)
	case c.floor.ok:
		query.FloorSlot, query.HasFloor = c.floor.slot, true
		newest = chain.AccountSignature{Slot: c.floor.slot}
		result.syncedFrom = c.floor.slot
	default:
		query.MaxPages = c.settings.MaxPages
	}
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}
	history, err := c.reader.AccountSignatures(ctx, account, query)
	if err != nil {
		result.err = fmt.Errorf("read the signatures of %s: %w", account, err)
		return result
	}
	if history.Truncated {
		if query.HasFloor {
			result.err = fmt.Errorf("the history of %s since slot %d is longer than %d pages; nothing was recorded and the cursor stays (scan the account's transactions with scan:deposits --tx)",
				account, query.FloorSlot, query.MaxPages)
			return result
		}
		if len(history.Signatures) > 0 {
			result.syncedFrom = history.Signatures[0].Slot
		}
		slog.Warn("deposit signature history cut at the page limit; older signatures are not read",
			"chain", c.chainID, "account", account, "max_pages", query.MaxPages, "oldest_slot_read", result.syncedFrom)
	}
	result.signatures = len(history.Signatures)

	start := newest
	for _, signature := range history.Signatures {
		if err := ctx.Err(); err != nil {
			result.err = err
			break
		}
		if !signature.Failed {
			deposits, err := c.record(ctx, signature)
			result.deposits += deposits
			if err != nil {
				result.err = err
				break
			}
		}
		newest = signature
	}
	if result.err != nil && newest == start {
		return result
	}
	if cursor != nil && newest == start {
		return result
	}
	if err := c.saveCursor(ctx, account, newest, result.syncedFrom); err != nil {
		result.err = errors.Join(result.err, err)
	}
	return result
}

// storedSlot is a slot read from the cursor store, which never returns a negative
// one (DepositSignatureCursorRepository.FindByChain refuses it).
func storedSlot(slot int64) uint64 {
	if slot < 0 {
		return 0
	}
	return uint64(slot)
}

// saveCursor stores account's cursor at signature.
func (c *signatureCycle) saveCursor(ctx context.Context, account string, signature chain.AccountSignature, syncedFrom uint64) error {
	if signature.Slot > math.MaxInt64 || syncedFrom > math.MaxInt64 {
		return fmt.Errorf("signature cursor of %s: slot %d does not fit a bigint", account, signature.Slot)
	}
	if err := c.svc.signatureCursors.Advance(ctx, models.DepositSignatureCursor{
		Chain: c.chainID, Account: account, Signature: signature.Signature,
		Slot: int64(signature.Slot), SyncedFromSlot: int64(syncedFrom),
	}); err != nil {
		return fmt.Errorf("save the signature cursor of %s: %w", account, err)
	}
	return nil
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
		slog.Info("deposit block scan resumed from its checkpoint: the signature sync is off for the chain", "chain", chainID)
	}
	return paused
}
