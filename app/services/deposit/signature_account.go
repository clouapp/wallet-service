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
	// signatures above an account's newest recorded one listed in one cycle. A burst
	// longer than that (100 000 transactions in one interval) is recorded newest first
	// as a stretch of its own ahead of the cursor, and the gap below it is read over the
	// next cycles (readNew).
	signatureForwardPageCeiling = 100
	// signatureBackfillPages is how many pages of an account's older history, or of
	// the gap below a burst, one cycle reads: a longer one is read over several cycles.
	signatureBackfillPages = 5
	// signatureCursorSaveEvery is how many signatures are recorded between two saves
	// of an account's cursor.
	signatureCursorSaveEvery = 50
	// signatureCursorSaveTimeout bounds one save, which does not depend on the cycle's
	// context: a cycle cut short still keeps what it recorded.
	signatureCursorSaveTimeout = 5 * time.Second
	// signatureIndexMargin is how many slots below an endpoint's finalized head its
	// address index may not have served yet: the head (getSlot) and the listing
	// (getSignaturesForAddress) are two calls, and one URL can front several backends
	// (a load balancer), or an index that trails the root. An account with nothing
	// recorded is known empty only up to the head less this margin, and an account read
	// up to a head counts as read up to the head less this margin (covered_head), so a
	// first deposit that lands just below the head is read on a later cycle instead of
	// falling under its floor. The margin is about one sync interval at the default 600
	// s (1500 slots of 400 ms, about 10 minutes): an index more than that behind could
	// still drop a first deposit, a wider margin costs only lag. covered_head (and
	// /health's lag_blocks) trails the head by at least the margin, the block scan
	// resumed after the sync rescans that many slots once, and a known-empty account
	// lists that many slots each cycle (an empty page while it stays empty). It does
	// not follow the configured interval: how far an index trails does not depend on
	// how often it is read.
	signatureIndexMargin = 1500
)

// accountSync is what reading one account did: whether it started (not when the
// cycle's budget was spent or its lock lost before it), the signatures it read, the
// deposits it recorded first in this cycle, the floor its history is read down to,
// the slot every signature up to which is recorded (coveredTo: 0 while the account is
// not read down to its floor, a gap above its cursor is still read, or the cycle left
// it behind), and why it stopped early.
type accountSync struct {
	started    bool
	signatures int
	deposits   int
	syncedFrom uint64
	coveredTo  uint64
	err        error
}

// accountState is an account's cursor while a cycle moves it: the recorded stretch of
// its history from top (the newest recorded signature, at topSlot; empty: none yet,
// topSlot is then the slot the account is known empty up to) down to bottom (the
// oldest recorded one), the floor the backfill goes down to, whether it got there and
// whether the last backfill read showed the end once (endSeen); and, after a burst,
// the stretch recorded ahead of a gap above top, from ahead (at aheadSlot) down to
// aheadBottom. bottomAnchor and aheadAnchor are the signatures recorded right above
// bottom and aheadBottom (empty: the end is the only signature of its stretch, or the
// cursor was saved before anchors were recorded): a listing below an end reads from
// its anchor and must find the end first (chain.SignatureQuery.Anchor).
type accountState struct {
	account      string
	top          string
	topSlot      uint64
	bottom       string
	bottomAnchor string
	ahead        string
	aheadSlot    uint64
	aheadBottom  string
	aheadAnchor  string
	floor        uint64
	hasFloor     bool
	covered      bool
	endSeen      bool
	stored       bool
	unsaved      int
}

func newAccountState(account string, row *models.DepositSignatureCursor, floor signatureFloor) *accountState {
	if row == nil {
		return &accountState{account: account, floor: floor.slot, hasFloor: floor.ok, topSlot: floor.slot}
	}
	return &accountState{account: account, top: row.Signature, topSlot: storedSlot(row.Slot), bottom: row.BackfillBefore,
		bottomAnchor: row.BackfillAnchor, ahead: row.AheadSignature, aheadSlot: storedSlot(row.AheadSlot), aheadBottom: row.AheadBefore,
		aheadAnchor: row.AheadAnchor, floor: storedSlot(row.SyncedFromSlot), hasFloor: true, covered: row.Covered, endSeen: row.EndSeen,
		stored: true}
}

// storedSlot is a slot read from the cursor store, which never returns a negative
// one (DepositSignatureCursorRepository.FindByChain refuses it).
func storedSlot(slot int64) uint64 {
	if slot < 0 {
		return 0
	}
	return uint64(slot)
}

// indexedUpTo is the slot a listing that ended at an endpoint's head proves read: the
// head less signatureIndexMargin (0 for a head within it).
func indexedUpTo(head uint64) uint64 {
	if head <= signatureIndexMargin {
		return 0
	}
	return head - signatureIndexMargin
}

// syncAccount reads one account, from its stored cursor or, without one, from floor:
// first the signatures above its newest recorded one
// (none until something was recorded, unless the account is known empty), then,
// until it is read down to its floor, at most signatureBackfillPages pages of its
// older history, newest first. Each signature moves the recorded stretch once its
// transfers are all recorded; a failure stops the account there. The cursor is saved
// every signatureCursorSaveEvery signatures and when the account stops, whatever
// stopped it.
func (c *signatureCycle) syncAccount(ctx context.Context, account string, floor signatureFloor) (result accountSync) {
	if !c.hold.Held() {
		result.err = ErrScanLockLost
		return result
	}
	if !c.svc.now().Before(c.deadline) {
		return result
	}
	result.started = true
	var row *models.DepositSignatureCursor
	if stored, ok := c.cursors[account]; ok {
		row = &stored
	}
	st := newAccountState(account, row, floor)
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

	head, upToDate := uint64(0), false
	if st.top != "" || st.covered || st.ahead != "" {
		var goOn bool
		head, upToDate, goOn = c.readNew(ctx, st, stopAt, &result)
		if !goOn {
			return result
		}
	}

	if !st.covered {
		nothingRecorded := st.top == "" && st.ahead == ""
		noFloor := !st.stored && !st.hasFloor
		pages := signatureBackfillPages
		if noFloor {
			pages = c.settings.MaxPages
		}
		before, anchor := c.placeBottom(st)
		history, err := c.reader.AccountSignatures(ctx, account, chain.SignatureQuery{
			Before: before, Anchor: anchor, FloorSlot: st.floor, HasFloor: st.hasFloor, MaxPages: pages,
		})
		if err != nil {
			result.err = fmt.Errorf("read the history of %s: %w", account, err)
			return result
		}
		result.signatures += len(history.Signatures)
		if noFloor {
			c.fixFloor(st, history, pages)
		}
		if !c.process(ctx, st, history.Signatures, moveDown, stopAt, &result) {
			return result
		}
		c.judgeHistoryEnd(st, history, noFloor)
		if st.covered && nothingRecorded {
			head, upToDate = history.Head, true
			st.markEmptyUpTo(history.Head)
		}
	}
	// Only an account read down to its floor, and up to a head, is read up to it.
	if st.covered && upToDate {
		result.coveredTo = indexedUpTo(head)
	}
	return result
}

// readNew records the signatures above the account's newest recorded one: above its
// cursor or, while a gap is open, above the stretch recorded ahead of the gap, oldest
// first, once the listing proves it reached them (reachedEnd); then down the gap. It
// returns the head the account is read up to with upToDate when nothing above its
// cursor is left unread, and goOn false when the account stops here (a failure, its
// budget, or an empty listing whose end nothing proves).
func (c *signatureCycle) readNew(ctx context.Context, st *accountState, stopAt time.Time, result *accountSync) (head uint64, upToDate, goOn bool) {
	query := chain.SignatureQuery{Until: st.top, FloorSlot: st.topSlot, HasFloor: true, MaxPages: signatureForwardPageCeiling}
	if st.ahead != "" {
		query.Until, query.FloorSlot = st.ahead, st.aheadSlot
	}
	history, err := c.reader.AccountSignatures(ctx, st.account, query)
	if err != nil {
		result.err = fmt.Errorf("read the new signatures of %s: %w", st.account, err)
		return 0, false, false
	}
	result.signatures += len(history.Signatures)
	switch {
	case reachedEnd(query, history): // never a listing MaxPages cut (More)
		oldestFirst := make([]chain.AccountSignature, len(history.Signatures))
		for i, signature := range history.Signatures {
			oldestFirst[len(history.Signatures)-1-i] = signature
		}
		if !c.process(ctx, st, oldestFirst, moveUp, stopAt, result) {
			return 0, false, false
		}
		st.markEmptyUpTo(history.Head)
	case len(history.Signatures) == 0:
		slog.Warn("deposit signature sync: the endpoint did not prove the end of an account's new signatures; its coverage waits for the next cycle",
			"chain", c.chainID, "account", st.account, "ledger_start", history.LedgerStart, "from_slot", query.FloorSlot)
		return 0, false, false
	default:
		// Nothing proves the listing reached the newest recorded signature (or, for an
		// account known empty, the slot it is known empty up to): a burst longer than a
		// cycle lists, or an endpoint that does not show what lies right above it (one
		// without the old ledger, an address index behind, a provider that answers a
		// signature it does not hold with an empty page). Moving the cursor over the
		// listing could skip what the endpoint did not show, so the listing is recorded
		// newest first as a stretch of its own ahead of the cursor, and the gap below it
		// is read down to the cursor, this cycle and the next, until an endpoint proves
		// it. Another such listing while the gap of the last one is read starts a new
		// stretch: the one recorded ahead of that gap becomes part of the larger gap,
		// read again (recording is idempotent), so no burst ever fails the account.
		if st.ahead != "" {
			st.ahead, st.aheadSlot, st.aheadBottom, st.aheadAnchor = "", 0, "", ""
			st.unsaved++
		}
		slog.Warn("deposit signature sync: new signatures above an account's cursor not proven to reach it (a burst, or an endpoint that does not show them all); recorded as a stretch ahead of the cursor, the gap below it over the next cycles",
			"chain", c.chainID, "account", st.account, "burst", history.More, "max_pages", signatureForwardPageCeiling, "ledger_start", history.LedgerStart)
		if !c.process(ctx, st, history.Signatures, moveGapDown, stopAt, result) {
			return 0, false, false
		}
	}
	head = history.Head
	if st.ahead == "" {
		return head, true, true
	}

	gapBefore, gapAnchor := c.placeAheadBottom(st)
	gapQuery := chain.SignatureQuery{Until: st.top, Before: gapBefore, Anchor: gapAnchor, FloorSlot: st.topSlot, HasFloor: true, MaxPages: signatureBackfillPages}
	gap, err := c.reader.AccountSignatures(ctx, st.account, gapQuery)
	if err != nil {
		result.err = fmt.Errorf("read the gap above the cursor of %s: %w", st.account, err)
		return 0, false, false
	}
	result.signatures += len(gap.Signatures)
	if !c.process(ctx, st, gap.Signatures, moveGapDown, stopAt, result) {
		return 0, false, false
	}
	if !reachedEnd(gapQuery, gap) {
		return 0, false, true
	}
	st.closeGap()
	return head, true, true
}

// placeBottom is the before and anchor of a listing below the account's oldest
// recorded signature (both empty while nothing is recorded: the listing reads from
// the newest signature). A stretch of one signature has nothing recorded above its
// bottom: the anchor is empty and the listing reads from the newest signature, which
// must then be the bottom. A stretch of several whose bottom has no anchor (a cursor
// saved before anchors were recorded) is first shrunk to its newest signature: what
// the cursor claims recorded only shrinks, and the signatures below it are read
// again (recording is idempotent) from an end the address index can place.
func (c *signatureCycle) placeBottom(st *accountState) (before, anchor string) {
	if st.bottom != "" && st.bottom != st.top && st.bottomAnchor == "" {
		slog.Info("deposit signature sync: an account's oldest recorded signature has no anchor; its history is read again from its newest recorded signature",
			"chain", c.chainID, "account", st.account)
		st.bottom, st.endSeen = st.top, false
		st.unsaved++
	}
	return st.bottom, st.bottomAnchor
}

// placeAheadBottom is the before and anchor of the listing of the gap below the
// stretch recorded ahead of the cursor, by the rule of placeBottom: a stretch of
// several without an anchor is shrunk to its newest signature, and the gap below it
// grows by what it gave up.
func (c *signatureCycle) placeAheadBottom(st *accountState) (before, anchor string) {
	if st.aheadBottom != st.ahead && st.aheadAnchor == "" {
		slog.Info("deposit signature sync: the oldest signature of a stretch ahead of a gap has no anchor; the gap is read again from its newest signature",
			"chain", c.chainID, "account", st.account)
		st.aheadBottom = st.ahead
		st.unsaved++
	}
	return st.aheadBottom, st.aheadAnchor
}

// reachedEnd reports whether a listing above an account's newest recorded signature
// (or of the gap below a burst) proved it read everything down to query.Until: the
// short page that reaches the signature; above a slot the account is known empty up
// to, only the floor or a ledger that holds that slot proves it.
func reachedEnd(query chain.SignatureQuery, history chain.SignatureHistory) bool {
	if !history.Ended {
		return false
	}
	return query.Until != "" || history.FloorCrossed || history.LedgerStart <= query.FloorSlot
}

// judgeHistoryEnd decides, after a backfill read, whether the account is read down to
// its floor: a signature below the floor proves it at once; an empty page from an
// endpoint whose ledger reaches the floor proves it only when the account's last
// backfill read showed it too and this one found nothing older (endSeen), so one odd
// answer (a backend without the account's index behind a load balancer) never covers
// an account for good. An account first read on a chain without a floor is covered
// when MaxPages cut its history (fixFloor).
func (c *signatureCycle) judgeHistoryEnd(st *accountState, history chain.SignatureHistory, noFloor bool) {
	endShown := history.Ended && !history.FloorCrossed && history.LedgerStart <= st.floor
	switch {
	case history.FloorCrossed:
		st.covered = true
	case endShown && st.endSeen && len(history.Signatures) == 0:
		st.covered = true
	case endShown:
		slog.Info("deposit signature history: the endpoint shows the end of an account's history; it is covered once the next cycle shows it again",
			"chain", c.chainID, "account", st.account, "ledger_start", history.LedgerStart, "floor", st.floor)
	case history.Ended:
		slog.Warn("deposit signature history: the endpoint's ledger starts above the account's floor, so nothing proves its older signatures are read; the account is read again on the next cycle",
			"chain", c.chainID, "account", st.account, "ledger_start", history.LedgerStart, "floor", st.floor)
	case history.More && noFloor:
		st.covered = true
	}
	if st.covered {
		st.unsaved++
	}
	if endShown != st.endSeen {
		st.endSeen = endShown
		st.unsaved++
	}
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

// stretchMove is how recording a signature moves an account's recorded stretches.
type stretchMove int

const (
	// moveUp: a new signature above the newest recorded one, read oldest first; it
	// tops the stretch ahead of a gap while there is one.
	moveUp stretchMove = iota
	// moveDown: an older signature below the oldest recorded one, read newest first.
	moveDown
	// moveGapDown: a signature of a burst, or of the gap below it, read newest first:
	// it extends the stretch ahead of the gap downward.
	moveGapDown
)

// process records signatures in order and moves the recorded stretches with each
// (move). It stops at the first failure, at stopAt (not a failure: the next cycle
// goes on), when ctx ends and once the chain's scan lock is found lost (checked
// before each signature it records), and saves the cursor every
// signatureCursorSaveEvery signatures. It reports whether it went through all of them.
func (c *signatureCycle) process(ctx context.Context, st *accountState, signatures []chain.AccountSignature, move stretchMove, stopAt time.Time, result *accountSync) bool {
	for _, signature := range signatures {
		if err := ctx.Err(); err != nil {
			result.err = err
			return false
		}
		if !c.svc.now().Before(stopAt) {
			return false
		}
		if !signature.Failed {
			if !c.hold.Held() {
				result.err = ErrScanLockLost
				return false
			}
			deposits, err := c.record(ctx, signature)
			result.deposits += deposits
			if err != nil {
				result.err = err
				return false
			}
		}
		st.advance(signature, move)
		if st.unsaved >= signatureCursorSaveEvery {
			if err := c.saveState(ctx, st); err != nil {
				result.err = err
				return false
			}
		}
	}
	return true
}

// advance moves the recorded stretches over signature, recorded, and keeps each end's
// anchor: listings are contiguous runs of the history, so a signature recorded below
// an end (moveDown, moveGapDown) is right below it, and the first one recorded above a
// stretch of one signature (moveUp, oldest first) is right above it.
func (st *accountState) advance(signature chain.AccountSignature, move stretchMove) {
	switch move {
	case moveUp:
		if st.ahead != "" {
			if st.aheadBottom == st.ahead && st.aheadAnchor == "" {
				st.aheadAnchor = signature.Signature
			}
			st.ahead, st.aheadSlot = signature.Signature, signature.Slot
			break
		}
		if st.top != "" && st.bottom == st.top && st.bottomAnchor == "" {
			st.bottomAnchor = signature.Signature
		}
		st.top, st.topSlot = signature.Signature, signature.Slot
		if st.bottom == "" {
			st.bottom = signature.Signature
		}
	case moveDown:
		st.bottomAnchor = st.bottom
		st.bottom = signature.Signature
		if st.top == "" {
			st.top, st.topSlot = signature.Signature, signature.Slot
		}
	case moveGapDown:
		st.aheadAnchor = st.aheadBottom
		st.aheadBottom = signature.Signature
		if st.ahead == "" {
			st.ahead, st.aheadSlot = signature.Signature, signature.Slot
		}
	}
	st.unsaved++
}

// closeGap joins the stretch recorded ahead of a gap to the cursor once the gap is
// read: its newest signature becomes the account's newest recorded one. The gap read
// ended right above top, so the stretch's oldest signature is the anchor of a bottom
// that was the cursor's only signature.
func (st *accountState) closeGap() {
	switch {
	case st.bottom == "":
		st.bottom, st.bottomAnchor = st.aheadBottom, st.aheadAnchor
	case st.bottom == st.top && st.bottomAnchor == "":
		st.bottomAnchor = st.aheadBottom
	}
	st.top, st.topSlot = st.ahead, st.aheadSlot
	st.ahead, st.aheadSlot, st.aheadBottom, st.aheadAnchor = "", 0, "", ""
	st.unsaved++
}

// markEmptyUpTo raises the slot an account with nothing recorded is known empty up to,
// to what a listing that ended at head proves (indexedUpTo); it never lowers it.
func (st *accountState) markEmptyUpTo(head uint64) {
	if st.top != "" || st.ahead != "" {
		return
	}
	if slot := indexedUpTo(head); slot > st.topSlot {
		st.topSlot = slot
		st.unsaved++
	}
}

// saveState stores the account's cursor when it moved since the last save, with a
// context detached from the cycle's (signatureCursorSaveTimeout), so a cycle cut by
// its deadline or a shutdown keeps what it recorded. An account that recorded nothing
// and learned nothing stores no row.
func (c *signatureCycle) saveState(ctx context.Context, st *accountState) error {
	if st.unsaved == 0 {
		return nil
	}
	if st.topSlot > math.MaxInt64 || st.floor > math.MaxInt64 || st.aheadSlot > math.MaxInt64 {
		return fmt.Errorf("signature cursor of %s: a slot does not fit a bigint", st.account)
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), signatureCursorSaveTimeout)
	defer cancel()
	if err := c.svc.signatureCursors.Advance(saveCtx, models.DepositSignatureCursor{
		Chain: c.chainID, Account: st.account, Signature: st.top, Slot: int64(st.topSlot),
		BackfillBefore: st.bottom, BackfillAnchor: st.bottomAnchor, SyncedFromSlot: int64(st.floor), Covered: st.covered, EndSeen: st.endSeen,
		AheadSignature: st.ahead, AheadSlot: int64(st.aheadSlot), AheadBefore: st.aheadBottom, AheadAnchor: st.aheadAnchor,
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
