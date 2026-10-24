package deposit

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

const (
	// signatureForwardPageCeiling bounds, in pages of 1000 signatures, the new
	// signatures above an account's cursor read in one cycle. More than that (a burst
	// of 100 000 transactions in one interval) fails the account: what lies right
	// above the cursor is on the last page, and nothing may be recorded past it.
	signatureForwardPageCeiling = 100
	// signatureBackfillPages is how many pages of an account's older history one
	// cycle reads: a history longer than that is read over several cycles.
	signatureBackfillPages = 5
	// signatureCursorSaveEvery is how many signatures are recorded between two saves
	// of an account's cursor.
	signatureCursorSaveEvery = 50
	// signatureCursorSaveTimeout bounds one save, which does not depend on the cycle's
	// context: a cycle cut short still keeps what it recorded.
	signatureCursorSaveTimeout = 5 * time.Second
)

// accountSync is what reading one account did: the signatures it read, the deposits
// it recorded first in this cycle, the floor its history is read down to, the slot
// every signature up to which is recorded (coveredTo: 0 while the account is not read
// down to its floor, or the cycle left it behind), and why it stopped early.
type accountSync struct {
	signatures int
	deposits   int
	syncedFrom uint64
	coveredTo  uint64
	err        error
}

// accountState is an account's cursor while a cycle moves it: the recorded stretch of
// its history from top (the newest recorded signature, at topSlot; empty: none yet,
// topSlot is then the slot the account is known empty up to) down to bottom (the
// oldest recorded one), the floor the backfill goes down to, and whether it got there.
type accountState struct {
	account  string
	top      string
	topSlot  uint64
	bottom   string
	floor    uint64
	hasFloor bool
	covered  bool
	stored   bool
	unsaved  int
}

func newAccountState(account string, row *models.DepositSignatureCursor, floor signatureFloor) *accountState {
	if row == nil {
		return &accountState{account: account, floor: floor.slot, hasFloor: floor.ok, topSlot: floor.slot}
	}
	return &accountState{account: account, top: row.Signature, topSlot: storedSlot(row.Slot), bottom: row.BackfillBefore,
		floor: storedSlot(row.SyncedFromSlot), hasFloor: true, covered: row.Covered, stored: true}
}

// storedSlot is a slot read from the cursor store, which never returns a negative
// one (DepositSignatureCursorRepository.FindByChain refuses it).
func storedSlot(slot int64) uint64 {
	if slot < 0 {
		return 0
	}
	return uint64(slot)
}

// syncAccount reads one account: first the signatures above its cursor, oldest first
// (none until something was recorded, unless the account is known empty), then, until
// it is read down to its floor, at most signatureBackfillPages pages of its older
// history, newest first. Each signature moves the recorded stretch once its transfers
// are all recorded; a failure stops the account there. The cursor is saved every
// signatureCursorSaveEvery signatures and when the account stops, whatever stopped it.
func (c *signatureCycle) syncAccount(ctx context.Context, account string, row *models.DepositSignatureCursor) (result accountSync) {
	if !c.hold.Held() {
		result.err = ErrScanLockLost
		return result
	}
	if !c.svc.now().Before(c.deadline) {
		return result
	}
	st := newAccountState(account, row, c.floor)
	stopAt := c.svc.now().Add(signatureAccountBudget)
	if c.deadline.Before(stopAt) {
		stopAt = c.deadline
	}
	defer func() {
		result.syncedFrom = st.floor
		if err := c.saveState(ctx, st); err != nil {
			result.err = joinErr(result.err, err)
			result.coveredTo = 0
		}
	}()

	head, readUpTo := uint64(0), false
	if st.top != "" || st.covered {
		query := chain.SignatureQuery{Until: st.top, FloorSlot: st.topSlot, HasFloor: true, MaxPages: signatureForwardPageCeiling}
		history, err := c.reader.AccountSignatures(ctx, account, query)
		if err != nil {
			result.err = fmt.Errorf("read the new signatures of %s: %w", account, err)
			return result
		}
		if history.More {
			result.err = fmt.Errorf("more than %d pages of new signatures above the cursor of %s; nothing was recorded", signatureForwardPageCeiling, account)
			return result
		}
		result.signatures += len(history.Signatures)
		newestFirst := history.Signatures
		oldestFirst := make([]chain.AccountSignature, len(newestFirst))
		for i, signature := range newestFirst {
			oldestFirst[len(newestFirst)-1-i] = signature
		}
		if !c.process(ctx, st, oldestFirst, true, stopAt, &result) {
			return result
		}
		// Above a cursor the short page reaches it; above a slot the account is known
		// empty up to, only the floor or a ledger that holds that slot proves the end.
		reached := history.Ended && (query.Until != "" || history.FloorCrossed || history.LedgerStart <= query.FloorSlot)
		if !reached {
			slog.Warn("deposit signature sync: the endpoint did not prove the end of an account's new signatures; its coverage waits for the next cycle",
				"chain", c.chainID, "account", account, "ledger_start", history.LedgerStart, "from_slot", query.FloorSlot)
			return result
		}
		if st.top == "" && st.topSlot < history.Head {
			st.topSlot, st.unsaved = history.Head, st.unsaved+1
		}
		head, readUpTo = history.Head, true
	}

	if !st.covered {
		readUpTo = false
		noFloor := !st.stored && !st.hasFloor
		pages := signatureBackfillPages
		if noFloor {
			pages = c.settings.MaxPages
		}
		history, err := c.reader.AccountSignatures(ctx, account, chain.SignatureQuery{
			Before: st.bottom, FloorSlot: st.floor, HasFloor: st.hasFloor, MaxPages: pages,
		})
		if err != nil {
			result.err = fmt.Errorf("read the history of %s: %w", account, err)
			return result
		}
		result.signatures += len(history.Signatures)
		if noFloor {
			c.fixFloor(st, history, pages)
		}
		firstRead := st.top == ""
		if !c.process(ctx, st, history.Signatures, false, stopAt, &result) {
			return result
		}
		switch {
		case history.FloorCrossed:
			st.covered = true
		case history.Ended && history.LedgerStart <= st.floor:
			st.covered = true
		case history.Ended:
			slog.Warn("deposit signature history: the endpoint's ledger starts above the account's floor, so nothing proves its older signatures are read; the account is read again on the next cycle",
				"chain", c.chainID, "account", account, "ledger_start", history.LedgerStart, "floor", st.floor)
		case history.More && noFloor:
			st.covered = true
		}
		if st.covered {
			st.unsaved++
			if firstRead {
				head, readUpTo = history.Head, true
				if st.top == "" {
					st.topSlot = history.Head
				}
			} else if head != 0 {
				readUpTo = true
			}
		}
	}
	if st.covered && readUpTo {
		result.coveredTo = head
	}
	return result
}

// fixFloor sets the floor of an account first read on a chain without one: the oldest
// slot read when MaxPages cut its history (logged), else the first block the endpoint
// holds (or the oldest slot read, when lower).
func (c *signatureCycle) fixFloor(st *accountState, history chain.SignatureHistory, pages int) {
	oldest := history.Head
	if n := len(history.Signatures); n > 0 {
		oldest = history.Signatures[n-1].Slot
	}
	switch {
	case history.More:
		st.floor = oldest
		slog.Warn("deposit signature history cut at the page limit; older signatures are not read",
			"chain", c.chainID, "account", st.account, "max_pages", pages, "oldest_slot_read", oldest)
	case history.Ended:
		st.floor = min(history.LedgerStart, oldest)
	default:
		st.floor = oldest
	}
	st.hasFloor = true
	if st.top == "" {
		st.topSlot = st.floor
	}
}

// process records signatures in order and moves the recorded stretch with each:
// upward (forward: each one the new top) or downward (backfill: each one the new
// bottom, the first one also the top while none was). It stops at the first failure,
// at stopAt (not a failure: the next cycle goes on) and when ctx ends, and saves the
// cursor every signatureCursorSaveEvery signatures. It reports whether it went
// through all of them.
func (c *signatureCycle) process(ctx context.Context, st *accountState, signatures []chain.AccountSignature, forward bool, stopAt time.Time, result *accountSync) bool {
	for _, signature := range signatures {
		if err := ctx.Err(); err != nil {
			result.err = err
			return false
		}
		if !c.svc.now().Before(stopAt) {
			return false
		}
		if !signature.Failed {
			deposits, err := c.record(ctx, signature)
			result.deposits += deposits
			if err != nil {
				result.err = err
				return false
			}
		}
		st.advance(signature, forward)
		if st.unsaved >= signatureCursorSaveEvery {
			if err := c.saveState(ctx, st); err != nil {
				result.err = err
				return false
			}
		}
	}
	return true
}

// advance moves the recorded stretch over signature, recorded.
func (st *accountState) advance(signature chain.AccountSignature, forward bool) {
	if forward || st.top == "" {
		st.top, st.topSlot = signature.Signature, signature.Slot
	}
	if !forward || st.bottom == "" {
		st.bottom = signature.Signature
	}
	st.unsaved++
}

// saveState stores the account's cursor when it moved since the last save, with a
// context detached from the cycle's (signatureCursorSaveTimeout), so a cycle cut by
// its deadline or a shutdown keeps what it recorded. An account that recorded nothing
// and learned nothing stores no row.
func (c *signatureCycle) saveState(ctx context.Context, st *accountState) error {
	if st.unsaved == 0 {
		return nil
	}
	if st.topSlot > math.MaxInt64 || st.floor > math.MaxInt64 {
		return fmt.Errorf("signature cursor of %s: slot %d does not fit a bigint", st.account, st.topSlot)
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signatureCursorSaveTimeout)
	defer cancel()
	if err := c.svc.signatureCursors.Advance(saveCtx, models.DepositSignatureCursor{
		Chain: c.chainID, Account: st.account, Signature: st.top, Slot: int64(st.topSlot),
		BackfillBefore: st.bottom, SyncedFromSlot: int64(st.floor), Covered: st.covered,
	}); err != nil {
		return fmt.Errorf("save the signature cursor of %s: %w", st.account, err)
	}
	st.unsaved, st.stored = 0, true
	return nil
}

func joinErr(first, second error) error {
	if first == nil {
		return second
	}
	return fmt.Errorf("%w; %w", first, second)
}
