package deposit

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// countingCursors counts the saves of a cursor store.
type countingCursors struct {
	SignatureCursorStore
	mu    sync.Mutex
	saves int
}

func (c *countingCursors) Advance(ctx context.Context, cursor models.DepositSignatureCursor) error {
	c.mu.Lock()
	c.saves++
	c.mu.Unlock()
	return c.SignatureCursorStore.Advance(ctx, cursor)
}

// stepClock moves by step each time it is read.
type stepClock struct {
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now
	c.now = c.now.Add(c.step)
	return now
}

// advance moves the clock by d at once (time spent waiting on a provider).
func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func bobHistory(f *signatureFixture, from, to uint64) {
	for slot := from; slot <= to; slot++ {
		f.chain.history(sigBob, f.chain.tx(fmt.Sprintf("sig-%d", slot), slot, lamports(sigBob, 1+int64(slot%1000), 1)))
	}
}

// A history longer than a cycle reads (signatureBackfillPages pages) is read over
// several cycles, below the stretch already recorded, while new signatures keep
// coming above it; the pending blocks are dropped, and the slot every account is read
// up to recorded, only once every account got down to its floor.
func TestSync_Signatures_ReadsAHistoryLongerThanACycleOverSeveralCycles(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.pageSize = 1
	f.keepPending(t, 1001)
	bobHistory(f, 1001, 1012)

	first := f.sync(t)

	assert.Equal(t, signatureBackfillPages, first.Deposits, "one cycle reads its pages, the newest")
	assert.Equal(t, 3, first.BackfillingAccounts, "bob's history goes on; the end of alice's two showed once")
	assert.Zero(t, first.FailedAccounts, "an account still read back is no failure")
	assert.Zero(t, first.CoveredHead)
	assert.Equal(t, []uint64{1001}, f.pendingBlocks(t), "nothing is dropped before every account reached its floor")
	sameCursor(t, f.cursors(t)[sigBob], "sig-1012", 1012, "sig-1008", sigCheckpoint, false)

	f.chain.history(sigBob, f.chain.tx("sig-new", 1500, lamports(sigBob, 7, 1)))
	second := f.sync(t)

	assert.Equal(t, 1+signatureBackfillPages, second.Deposits, "the new signature above, five more below")
	sameCursor(t, f.cursors(t)[sigBob], "sig-new", 1500, "sig-1003", sigCheckpoint, false)
	queries := f.chain.queriesOf(sigBob)
	assert.Equal(t, chain.SignatureQuery{Until: "sig-1012", FloorSlot: 1012, HasFloor: true, MaxPages: signatureForwardPageCeiling}, queries[1])
	assert.Equal(t, chain.SignatureQuery{Before: "sig-1008", Anchor: "sig-1009", FloorSlot: sigCheckpoint, HasFloor: true, MaxPages: signatureBackfillPages}, queries[2],
		"below the oldest recorded signature, from the one recorded right above it")

	third := f.sync(t)

	assert.Equal(t, 2, third.Deposits)
	assert.Equal(t, 1, third.BackfillingAccounts, "the end of bob's history shows once, below the last two")
	assert.Zero(t, third.CoveredHead)
	sameCursor(t, f.cursors(t)[sigBob], "sig-new", 1500, "sig-1001", sigCheckpoint, false)

	fourth := f.sync(t)

	assert.Zero(t, fourth.BackfillingAccounts)
	assert.Equal(t, uint64(sigCovered), fourth.CoveredHead)
	assert.Equal(t, 1, fourth.ResolvedPending)
	assert.Empty(t, f.pendingBlocks(t))
	sameCursor(t, f.cursors(t)[sigBob], "sig-new", 1500, "sig-1001", sigCheckpoint, true)
	assert.Len(t, sigDeposits(t), 13, "every signature recorded once")
}

// An endpoint whose ledger does not reach the floor (a node that is not an archival
// one, a failover to one) answers the end of a history with an empty page and no
// error: nothing proves the older signatures are read, so the account is not
// covered, no pending block is dropped and no slot recorded, until an endpoint
// proves it.
func TestSync_Signatures_ClaimsNoCoverageAnEndpointCannotProve(t *testing.T) {
	logs := captureLogs(t)
	f := newSignatureFixture(t)
	f.keepPending(t, 1050)
	f.chain.ledgerStart = 1800
	bobHistory(f, 1900, 1901)

	first := f.sync(t)

	assert.Equal(t, 2, first.Deposits, "what the endpoint holds is recorded")
	assert.Equal(t, 3, first.BackfillingAccounts, "no account is proven read down to its floor")
	assert.Zero(t, first.CoveredHead)
	assert.Equal(t, []uint64{1050}, f.pendingBlocks(t))
	assert.False(t, f.cursors(t)[sigBob].Covered)
	assert.NotEmpty(t, recordsOf(logs(), "WARN", "deposit signature history: the endpoint's ledger starts above the account's floor, so nothing proves its older signatures are read; the account is read again on the next cycle"))

	f.chain.mu.Lock()
	f.chain.ledgerStart = 0
	f.chain.mu.Unlock()
	second := f.sync(t)

	assert.Equal(t, 3, second.BackfillingAccounts, "an endpoint that holds the ledger down to the floor shows the end once")
	assert.Zero(t, second.CoveredHead)
	assert.True(t, f.cursors(t)[sigBob].EndSeen)

	third := f.sync(t)

	assert.Zero(t, third.BackfillingAccounts, "and proves it when it shows it again")
	assert.Equal(t, uint64(sigCovered), third.CoveredHead)
	assert.Empty(t, f.pendingBlocks(t))
	assert.True(t, f.cursors(t)[sigBob].Covered)
}

// The end of a history an endpoint shows once and contradicts on the next cycle (a
// load-balanced URL whose backends differ) covers nothing: the account is covered
// only once two backfill reads in a row show the end and find nothing older.
func TestSync_Signatures_CoversAnAccountOnlyWhenTheEndShowsTwiceInARow(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.accounts[sigAlice] = []string{sigAlice}
	f.chain.history(sigBob, f.chain.tx("sig-2", 1202, lamports(sigBob, 20, 1)))

	first := f.sync(t)
	assert.Equal(t, 2, first.BackfillingAccounts)
	assert.True(t, f.cursors(t)[sigBob].EndSeen, "the end shows below sig-2")

	// The next backend holds an older signature the first one did not show.
	older := f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1))
	f.chain.mu.Lock()
	f.chain.histories[sigBob] = append([]chain.AccountSignature{older}, f.chain.histories[sigBob]...)
	f.chain.mu.Unlock()
	second := f.sync(t)

	assert.Equal(t, 1, second.Deposits, "the older signature is recorded")
	assert.False(t, f.cursors(t)[sigBob].Covered, "an end contradicted by the next read covers nothing")
	assert.True(t, f.cursors(t)[sigAlice].Covered, "alice's end showed twice")
	assert.Zero(t, second.CoveredHead)

	third := f.sync(t)

	assert.True(t, f.cursors(t)[sigBob].Covered)
	assert.Equal(t, uint64(sigCovered), third.CoveredHead)
	assert.Len(t, sigDeposits(t), 2)
}

// An account known empty up to a slot is read above it each cycle; an endpoint whose
// ledger starts above that slot proves nothing, so the cycle claims no coverage.
func TestSync_Signatures_ClaimsNoCoverageAnEndpointCannotProveAboveAnEmptyAccount(t *testing.T) {
	f := newSignatureFixture(t)
	first := f.syncCovered(t)
	require.Equal(t, uint64(sigCovered), first.CoveredHead)
	sameCursor(t, f.cursors(t)[sigBob], "", sigCovered, "", sigCheckpoint, true)

	f.chain.mu.Lock()
	f.chain.ledgerStart = sigCovered + 1
	f.chain.mu.Unlock()
	second := f.sync(t)

	assert.Equal(t, chain.SignatureQuery{FloorSlot: sigCovered, HasFloor: true, MaxPages: signatureForwardPageCeiling}, f.chain.queriesOf(sigBob)[2],
		"an account known empty is read above the slot it is known empty up to")
	assert.Zero(t, second.CoveredHead, "a ledger that starts above that slot proves nothing")
	assert.Equal(t, 3, second.BackfillingAccounts)
}

// An address index that trails the head by about seven minutes (1000 slots, within
// one default sync interval) drops no first deposit: an account it showed empty is
// known empty only up to the head less the index margin, so the deposit it served late
// is read on the next cycle.
func TestSync_Signatures_AnIndexMinutesBehindDropsNoFirstDeposit(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.accounts[sigAlice] = []string{sigAlice}
	late := uint64(sigHead - 1000)
	f.chain.history(sigBob, f.chain.tx("sig-late", late, lamports(sigBob, 42, 1)))
	f.chain.mu.Lock()
	f.chain.hideAbove = late - 1 // the index has not served the deposit yet
	f.chain.mu.Unlock()
	f.syncCovered(t)
	require.Empty(t, sigDeposits(t))

	f.chain.mu.Lock()
	f.chain.hideAbove = 0
	f.chain.mu.Unlock()
	f.sync(t)

	rows := sigDeposits(t)
	require.Len(t, rows, 1, "the deposit the index served late is recorded")
	assert.Equal(t, "sig-late", rows[0].TxHash)
}

// An account found empty is known empty only up to the head less the index margin:
// the endpoint's head and its listing are two calls, and its address index (or
// another backend behind the same URL) may not serve yet a signature just below that
// head. The first deposit to a fresh address that lands there is read on the next
// cycle, and the slot every account is read up to stays below it.
func TestSync_Signatures_AnEmptyAccountsFirstDepositJustBelowTheHeadIsRecorded(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.accounts[sigAlice] = []string{sigAlice}
	first := f.syncCovered(t)
	assert.Equal(t, uint64(sigCovered), first.CoveredHead, "not the head: a signature just below it may not be served yet")
	sameCursor(t, f.cursors(t)[sigBob], "", sigCovered, "", sigCheckpoint, true)

	// The first deposit to bob landed at slot 1995, but the index serves it only now.
	f.chain.history(sigBob, f.chain.tx("sig-late", sigHead-5, lamports(sigBob, 42, 1)))
	f.chain.mu.Lock()
	f.chain.heads[sigBob] = sigHead + 100
	f.chain.mu.Unlock()
	f.sync(t)

	rows := sigDeposits(t)
	require.Len(t, rows, 1, "bob's first deposit is recorded")
	assert.Equal(t, "sig-late", rows[0].TxHash)
	assert.Equal(t, chain.SignatureQuery{FloorSlot: sigCovered, HasFloor: true, MaxPages: signatureForwardPageCeiling}, f.chain.queriesOf(sigBob)[2])
	sameCursor(t, f.cursors(t)[sigBob], "sig-late", sigHead-5, "sig-late", sigCheckpoint, true)
}

// A cycle cut by its context (the 60 s Lambda deadline, SIGTERM on a deploy) keeps
// what each account recorded: its cursor is saved with a context of its own, so a
// deadline shorter than an account's history still moves it forward.
func TestSync_Signatures_ACycleCutByItsContextKeepsTheAccountsProgress(t *testing.T) {
	for name, tc := range map[string]struct {
		covered     bool
		cutAt       string
		wantTop     string
		wantBottom  string
		wantRecords []string
	}{
		"new signatures, oldest first": {covered: true, cutAt: "sig-3", wantTop: "sig-2", wantBottom: "sig-0", wantRecords: []string{"sig-0", "sig-1", "sig-2"}},
		"history, newest first":        {cutAt: "sig-1", wantTop: "sig-3", wantBottom: "sig-2", wantRecords: []string{"sig-2", "sig-3"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			if tc.covered {
				f.chain.history(sigBob, f.chain.tx("sig-0", 1200, lamports(sigBob, 1, 1)))
				f.sync(t)
			}
			f.chain.history(sigBob,
				f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)),
				f.chain.tx("sig-2", 1202, lamports(sigBob, 20, 1)),
				f.chain.tx("sig-3", 1203, lamports(sigBob, 30, 1)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.chain.onTx = func(_ context.Context, signature string) {
				if signature == tc.cutAt {
					cancel()
				}
			}
			f.chain.txErr[tc.cutAt] = context.Canceled

			_, err := f.svc.SyncSignatures(ctx, sigChain)

			require.Error(t, err)
			var recorded []string
			for _, row := range sigDeposits(t) {
				recorded = append(recorded, row.TxHash)
			}
			assert.Equal(t, tc.wantRecords, recorded)
			cursor, ok := f.cursors(t)[sigBob]
			require.True(t, ok, "bob's progress survives the cut")
			assert.Equal(t, tc.wantTop, cursor.Signature)
			assert.Equal(t, tc.wantBottom, cursor.BackfillBefore)
		})
	}
}

func TestSync_Signatures_SavesTheCursorAsItGoes(t *testing.T) {
	f := newSignatureFixture(t)
	counting := &countingCursors{SignatureCursorStore: f.svc.signatureCursors}
	f.svc.signatureCursors = counting
	bobHistory(f, 1001, 1001+2*signatureCursorSaveEvery+10)

	summary := f.sync(t)

	assert.Equal(t, 2*signatureCursorSaveEvery+11, summary.Deposits)
	counting.mu.Lock()
	defer counting.mu.Unlock()
	assert.GreaterOrEqual(t, counting.saves, 3+2, "bob's cursor every 50 signatures and when he stops, and alice's two accounts")
}

// Each account reads for at most signatureAccountBudget a cycle and the cycle for
// signatureBudgetOf its interval: an account stopped by it keeps what it recorded and goes on
// next cycle, and is no failure.
func TestSync_Signatures_StopsAnAccountAtItsTimeBudgetAndGoesOnNextCycle(t *testing.T) {
	f := newSignatureFixture(t)
	clock := &stepClock{now: activityNow, step: 30 * time.Second}
	f.svc.now = clock.Now
	// The clock moves on every read, whoever reads it: one owner at a time, so the
	// reads of alice's accounts do not spend bob's budget; a 10-minute interval leaves
	// the cycle a budget longer than an account's.
	f.settings.Concurrency = 1
	f.settings.Interval = 10 * time.Minute
	bobHistory(f, 1001, 1040)

	first := f.sync(t)

	assert.Zero(t, first.FailedAccounts)
	assert.Positive(t, first.BackfillingAccounts)
	recorded := len(sigDeposits(t))
	assert.Positive(t, recorded)
	assert.Less(t, recorded, 40, "the budget stops the account before its history ends")
	assert.False(t, f.cursors(t)[sigBob].Covered)

	for i := 0; i < 20 && !f.cursors(t)[sigBob].Covered; i++ {
		f.sync(t)
	}
	assert.True(t, f.cursors(t)[sigBob].Covered, "later cycles go on where the budget stopped")
	assert.Len(t, sigDeposits(t), 40)
}

// Under a context with a deadline (the deposit_scanner Lambda's) a cycle's budget ends
// signatureDeadlineMargin before it, and its reads are cut signatureSaveReserve before
// it, which leaves the final cursor saves their time.
func TestSync_Signatures_EndsItsBudgetBeforeItsContextsDeadline(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.accounts[sigAlice] = []string{sigAlice}
	f.svc.now = (&stepClock{now: activityNow, step: time.Second}).Now
	bobHistory(f, 1001, 1100)
	var mu sync.Mutex
	var readDeadlines []time.Time
	f.chain.onTx = func(ctx context.Context, _ string) {
		deadline, _ := ctx.Deadline()
		mu.Lock()
		defer mu.Unlock()
		readDeadlines = append(readDeadlines, deadline)
	}
	end := time.Now().Add(signatureDeadlineMargin + 30*time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), end)
	defer cancel()

	summary, err := f.svc.SyncSignatures(ctx, sigChain)

	require.NoError(t, err, "a budget that ends is no failure")
	assert.Positive(t, summary.BackfillingAccounts)
	recorded := len(sigDeposits(t))
	assert.Positive(t, recorded)
	assert.Less(t, recorded, 40, "about 30 s of the service's clock at a second a signature, not the account's 3 minutes")
	assert.Equal(t, fmt.Sprintf("sig-%d", 1100-recorded+1), f.cursors(t)[sigBob].BackfillBefore, "bob's progress is saved")
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, readDeadlines)
	for _, deadline := range readDeadlines {
		assert.True(t, deadline.Equal(end.Add(-signatureSaveReserve)), "the reads end signatureSaveReserve before the deadline, not %s", deadline)
	}
}

// A context too close to its deadline runs no cycle: it takes no lock and reads nothing.
func TestSync_Signatures_RunsNoCycleTooCloseToItsContextsDeadline(t *testing.T) {
	f := newSignatureFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), signatureDeadlineMargin-time.Second)
	defer cancel()

	_, err := f.svc.SyncSignatures(ctx, sigChain)

	require.ErrorIs(t, err, errSignatureNoTime)
	assert.Empty(t, f.chain.queriesOf(sigBob))
	assert.False(t, f.svc.cache.Has(scanLockKey(sigChain)), "the chain's scan lock is not taken")
}

// One registered mint the node does not hold as a token mint (a mainnet mint on a
// devnet chain) is skipped, named in the cycle and its record, and never stops the
// owners or the other mints.
func TestSync_Signatures_NamesTheRegisteredMintsTheNodeDoesNotHold(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.skippedMints = []string{"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"}
	f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
	activity := newMemoryActivity()
	f.svc.SetScanActivityStore(activity)

	summary := f.sync(t)

	assert.Equal(t, []string{"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"}, summary.SkippedMints)
	assert.Zero(t, summary.FailedAccounts)
	assert.Equal(t, 1, summary.Deposits)
	record, _ := activity.syncOf(sigChain)
	assert.Equal(t, summary.SkippedMints, record.SkippedMints)
}

// The slot every account is read up to is the lowest head an account's endpoint read
// it up to, and never above the head read before the addresses were listed (an
// address created later received nothing up to it). A cycle that did not read every
// account keeps the last one's of this period of signature mode in its record.
func TestSync_Signatures_RecordsTheSlotEveryAccountIsReadUpTo(t *testing.T) {
	f := newSignatureFixture(t)
	activity := newMemoryActivity()
	activity.modes[sigChain] = ScanModeRecord{Mode: ScanModeSignatures, Since: time.Now().Add(-time.Hour), At: time.Now()}
	f.svc.SetScanActivityStore(activity)
	f.chain.heads[sigAliceUSDC] = sigHead - 10
	f.chain.heads[sigBob] = sigHead + 100

	first := f.syncCovered(t)

	assert.Equal(t, uint64(sigCovered-10), first.CoveredHead, "the lowest endpoint head, less the index margin")
	record, _ := activity.syncOf(sigChain)
	assert.Equal(t, uint64(sigCovered-10), record.CoveredHead)
	assert.True(t, record.ReadEveryAccount)
	assert.Equal(t, uint64(sigHead), record.Head)

	f.chain.mu.Lock()
	ahead := uint64(sigHead + 2*signatureIndexMargin)
	f.chain.heads[sigAlice], f.chain.heads[sigAliceUSDC], f.chain.heads[sigBob] = ahead, ahead, ahead
	f.chain.mu.Unlock()
	capped := f.sync(t)
	assert.Equal(t, uint64(sigHead), capped.CoveredHead, "never above the head read before the addresses were listed")

	f.chain.mu.Lock()
	f.chain.accountErr[sigBob] = fmt.Errorf("429")
	f.chain.mu.Unlock()
	second := f.sync(t)

	assert.Zero(t, second.CoveredHead)
	record, _ = activity.syncOf(sigChain)
	assert.Equal(t, uint64(sigHead), record.CoveredHead, "carried from the last cycle that read every account, for the health check")
	assert.False(t, record.ReadEveryAccount, "but this cycle did not read them all")
	assert.Equal(t, 1, record.FailedAccounts)
	f.chain.GetLatestBlockFn = func(context.Context) (uint64, error) { return sigHead + 10, nil }
	f.sync(t)
	record, _ = activity.syncOf(sigChain)
	assert.Equal(t, uint64(10), record.LagBlocks(), "the head confirmed at, past the slot every account was last read up to")
}

// The deposit_scanner Lambda: a chain in signature mode reads no block, retries its
// due pending blocks, and syncs once its interval elapsed since the last completed
// cycle, not on every invocation.
func TestRun_ScanInvocation_SyncsAListedChainOnceItsIntervalElapsed(t *testing.T) {
	f := newSignatureFixture(t)
	activity := newMemoryActivity()
	f.svc.SetScanActivityStore(activity)
	clock := &testClock{now: activityNow}
	f.svc.now = clock.Now
	f.settings.Interval = 10 * time.Minute
	f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
	var scanned atomic.Int64
	f.chain.ScanBlockFn = func(context.Context, uint64) ([]types.DetectedTransfer, error) {
		scanned.Add(1)
		return nil, nil
	}
	ctx := context.Background()

	run, err := f.svc.RunScanInvocation(ctx, sigChain)
	require.NoError(t, err)
	assert.True(t, run.Synced, "no completed cycle yet: it syncs")
	assert.Equal(t, 1, run.Sync.Deposits)

	clock.advance(5 * time.Minute)
	run, err = f.svc.RunScanInvocation(ctx, sigChain)
	require.NoError(t, err)
	assert.False(t, run.Synced, "within the interval it does not")
	assert.Len(t, f.chain.queriesOf(sigBob), 1)

	clock.advance(6 * time.Minute)
	run, err = f.svc.RunScanInvocation(ctx, sigChain)
	require.NoError(t, err)
	assert.True(t, run.Synced, "past the interval it syncs again")
	assert.Zero(t, scanned.Load(), "no block is read for a chain in signature mode")

	f.settings.Chains = nil
	clock.advance(time.Hour)
	run, err = f.svc.RunScanInvocation(ctx, sigChain)
	require.NoError(t, err)
	assert.False(t, run.Synced, "a chain off the list is never synced by the scan")
}

// In signature mode the block scan reads no block, but its confirmation step still
// runs on the scan's cadence: a deposit the provider push recorded (the fast path)
// confirms within a few scan ticks instead of the next sync cycle.
func TestScan_LatestBlocks_ConfirmsAPushedDepositOfAChainInSignatureMode(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.tx("sig-pushed", 1300, lamports(sigAlice, 77, 1))
	ingestSvc := newIngestSvc(f.registry)
	ingestSvc.SetDepositEvents(f.events)
	require.NoError(t, ingestSvc.Ingest(context.Background(), ingest.InboundEvent{ChainID: sigChain, Transfers: []providers.InboundTransfer{
		{TxHash: "sig-pushed", BlockNumber: 1300, From: sigPayer, To: sigAlice, Amount: big.NewInt(77), LogIndex: 1},
	}}))
	probe := &lockProbe{svc: f.svc, chainID: sigChain}
	f.svc.SetBalanceRefresher(probe)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
		require.NoError(t, f.svc.RunWithdrawalConfirmationCheck(ctx))
	}

	rows := sigDeposits(t)
	require.Len(t, rows, 1)
	assert.Equal(t, string(types.TxStatusConfirmed), rows[0].Status, "the push is meant to be the fast path")
	assert.Len(t, f.events.ids[types.EventDepositConfirmed], 1)
	assert.Empty(t, f.chain.queriesOf(sigAlice), "the scan reads no signature either: that is the sync's")
	require.NotEmpty(t, probe.freeSeen, "alice's wallet is refreshed")
	for _, free := range probe.freeSeen {
		assert.True(t, free, "the balances are refreshed once the chain's scan lock is released")
	}
}

func TestScan_LatestBlocks_LeavesTheConfirmationsToASyncCycleThatHoldsTheChain(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.tx("sig-pushed", 1300, lamports(sigAlice, 77, 1))
	ingestSvc := newIngestSvc(f.registry)
	require.NoError(t, ingestSvc.Ingest(context.Background(), ingest.InboundEvent{ChainID: sigChain, Transfers: []providers.InboundTransfer{
		{TxHash: "sig-pushed", BlockNumber: 1300, From: sigPayer, To: sigAlice, Amount: big.NewInt(77), LogIndex: 1},
	}}))
	hold, err := f.svc.holdChain(sigChain)
	require.NoError(t, err)
	defer hold.Release()
	reads := 0
	f.chain.GetLatestBlockFn = func(context.Context) (uint64, error) {
		reads++
		return sigHead, nil
	}

	require.NoError(t, f.svc.ScanLatestBlocks(context.Background(), sigChain))

	assert.Zero(t, reads, "the sync cycle confirms at its end")
	assert.Equal(t, string(types.TxStatusPending), sigDeposits(t)[0].Status)
}

// The scan records, on every cycle it starts, how the chain's deposits are read, so
// the health check and the alerts follow the setting: a one-off sync of a chain the
// block scan reads (deposits:sync-signatures) never hides that scan's lag.
func TestScan_LatestBlocks_RecordsTheChainsDetectionMode(t *testing.T) {
	f := newSignatureFixture(t)
	activity := newMemoryActivity()
	f.svc.SetScanActivityStore(activity)
	clock := &testClock{now: activityNow}
	f.svc.now = clock.Now
	ctx := context.Background()

	require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
	mode, ok := activity.modeOf(sigChain)
	require.True(t, ok)
	assert.Equal(t, ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow, At: activityNow}, mode)

	clock.advance(time.Minute)
	require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
	mode, _ = activity.modeOf(sigChain)
	assert.Equal(t, activityNow, mode.At, "an unchanged mode is not written on every cycle")

	clock.advance(modeRefreshEvery)
	require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
	mode, _ = activity.modeOf(sigChain)
	assert.Equal(t, activityNow.Add(time.Minute+modeRefreshEvery), mode.At, "it is written again before it could expire")
	assert.Equal(t, activityNow, mode.Since, "keeping when the mode began")

	f.settings.Chains = nil
	clock.advance(time.Minute)
	require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
	mode, _ = activity.modeOf(sigChain)
	assert.Equal(t, ScanModeBlocks, mode.Mode)
	assert.Equal(t, clock.Now(), mode.Since)

	_, err := f.svc.SyncSignatures(ctx, sigChain)
	require.NoError(t, err)
	readings := f.svc.ScanActivityHealth(ctx)
	assert.False(t, readings.Chains[sigChain].SignatureMode(), "a one-off sync of a chain the block scan reads is not its mode")
}

// When the chain leaves deposit_signatures, the block scan resumes after the slot the
// sync's last cycle read every account up to, instead of the checkpoint it stalled
// at, and from that checkpoint when the last cycle did not read them all: a covered
// slot carried from an earlier cycle predates the accounts listed since.
func TestScan_LatestBlocks_ResumesAfterTheSlotTheSignatureSyncReadEveryAccountUpTo(t *testing.T) {
	for name, tc := range map[string]struct {
		covered uint64
		readAll bool
		want    uint64
	}{
		"every account read up to 1500":      {covered: 1500, readAll: true, want: 1500 + 5},
		"no cycle read them all":             {covered: 0, want: sigCheckpoint + 5},
		"carried from an earlier full cycle": {covered: 1500, readAll: false, want: sigCheckpoint + 5},
		"covered below the checkpoint":       {covered: 900, readAll: true, want: sigCheckpoint + 5},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			activity := newMemoryActivity()
			f.svc.SetScanActivityStore(activity)
			activity.modes[sigChain] = ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow, At: activityNow}
			activity.signatures[sigChain] = SignatureSyncRecord{Accounts: 3, CoveredHead: tc.covered, ReadEveryAccount: tc.readAll, Head: sigHead, At: activityNow}
			var read []uint64
			f.chain.ScanBlockFn = func(_ context.Context, block uint64) ([]types.DetectedTransfer, error) {
				read = append(read, block)
				return nil, nil
			}
			require.NoError(t, f.svc.SetScanOptions(ScanOptions{BatchBlocks: 5, CatchUpBlocks: 5, Concurrency: 1}))
			f.settings.Chains = nil

			require.NoError(t, f.svc.ScanLatestBlocks(context.Background(), sigChain))

			checkpoint, err := f.svc.checkpoints.Find(context.Background(), sigChain)
			require.NoError(t, err)
			assert.Equal(t, tc.want, checkpoint)
			require.Len(t, read, 5)
			assert.Equal(t, tc.want-4, read[0], "the first block read is right after the resume point")
			mode, _ := activity.modeOf(sigChain)
			assert.Equal(t, ScanModeBlocks, mode.Mode)

			require.NoError(t, f.svc.ScanLatestBlocks(context.Background(), sigChain))
			again, err := f.svc.checkpoints.Find(context.Background(), sigChain)
			require.NoError(t, err)
			assert.Equal(t, tc.want+5, again, "the resume happens once, on the change")

			// A one-off sync of the chain the block scan now reads moves no checkpoint.
			activity.signatures[sigChain] = SignatureSyncRecord{Accounts: 3, CoveredHead: sigHead - 50, ReadEveryAccount: true, Head: sigHead, At: activityNow}
			require.NoError(t, f.svc.ScanLatestBlocks(context.Background(), sigChain))
			later, err := f.svc.checkpoints.Find(context.Background(), sigChain)
			require.NoError(t, err)
			assert.Equal(t, tc.want+10, later, "the block scan goes on from where it read")
		})
	}
}

// A cycle that skipped a registered mint did not read that mint's accounts: it records
// no covered slot for the block scan to resume after and drops no pending block, so a
// deposit of that mint is still read, by the block scan or the pending retry, once the
// chain leaves deposit_signatures.
func TestSync_Signatures_ASkippedMintCoversNothing(t *testing.T) {
	for name, withPending := range map[string]bool{"resume point": false, "pending block": true} {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			activity := newMemoryActivity()
			f.svc.SetScanActivityStore(activity)
			ctx := context.Background()
			f.chain.history(sigAliceUSDC, f.chain.tx("sig-usdc", 1500, usdcCredit(sigAlice, 2_200, 3)))
			if withPending {
				f.keepPending(t, 1500)
			}
			// The node does not hold USDC as a token mint: alice's USDC account is not listed.
			f.chain.accounts[sigAlice] = []string{sigAlice}
			f.chain.skippedMints = []string{sigUSDCMint}
			require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))

			summary := f.syncCovered(t)

			assert.Zero(t, summary.CoveredHead, "the skipped mint's accounts were not read")
			assert.Zero(t, summary.ResolvedPending)
			record, _ := activity.syncOf(sigChain)
			assert.False(t, record.ReadEveryAccount)

			f.settings.Chains = nil
			require.NoError(t, f.svc.SetScanOptions(ScanOptions{BatchBlocks: 1000, CatchUpBlocks: 1000, Concurrency: 8}))
			require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
			_, err := f.svc.ReprocessPending(ctx, sigChain, true)
			require.NoError(t, err)

			rows := sigDeposits(t)
			require.Len(t, rows, 1, "the USDC deposit at slot 1500 is recorded by the block scan or the pending retry")
			assert.Equal(t, "sig-usdc", rows[0].TxHash)
		})
	}
}

// The covered slot carried from an earlier full cycle predates the accounts listed
// since (a token registered later): after a cycle that did not read every account the
// block scan resumes from its checkpoint, and reads their older deposits.
func TestScan_LatestBlocks_ResumesFromTheCheckpointAfterACycleThatDidNotReadEveryAccount(t *testing.T) {
	f := newSignatureFixture(t)
	activity := newMemoryActivity()
	f.svc.SetScanActivityStore(activity)
	ctx := context.Background()
	f.chain.accounts[sigAlice] = []string{sigAlice} // USDC not registered yet
	require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
	full := f.syncCovered(t)
	require.Equal(t, uint64(sigCovered), full.CoveredHead)

	// USDC is registered; alice's USDC account appears and its history fails once.
	f.chain.history(sigAliceUSDC, f.chain.tx("sig-usdc", 1500, usdcCredit(sigAlice, 2_200, 3)))
	f.chain.mu.Lock()
	f.chain.accounts[sigAlice] = []string{sigAlice, sigAliceUSDC}
	f.chain.txErr["sig-usdc"] = fmt.Errorf("429")
	f.chain.mu.Unlock()
	partial := f.sync(t)
	require.Equal(t, 1, partial.FailedAccounts)
	record, _ := activity.syncOf(sigChain)
	assert.Equal(t, uint64(sigCovered), record.CoveredHead, "carried for the health check")
	assert.False(t, record.ReadEveryAccount)

	f.settings.Chains = nil
	f.chain.mu.Lock()
	delete(f.chain.txErr, "sig-usdc")
	f.chain.mu.Unlock()
	require.NoError(t, f.svc.SetScanOptions(ScanOptions{BatchBlocks: 1000, CatchUpBlocks: 1000, Concurrency: 8}))
	require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))

	rows := sigDeposits(t)
	require.Len(t, rows, 1, "the USDC deposit at 1500, below the carried covered slot, is recorded by the block scan")
	assert.Equal(t, "sig-usdc", rows[0].TxHash)
}

func TestScan_LatestBlocks_PausesTheBlockReadsWhileTheSignatureSyncReadsTheChainButNotThePendingRetry(t *testing.T) {
	logs := captureLogs(t)
	f := newSignatureFixture(t)
	var mu sync.Mutex
	var read []uint64
	f.chain.ScanBlockFn = func(_ context.Context, block uint64) ([]types.DetectedTransfer, error) {
		mu.Lock()
		defer mu.Unlock()
		read = append(read, block)
		return nil, nil
	}
	now := time.Now().UTC()
	require.NoError(t, f.pending.Put(context.Background(), pendingEntryDue(990, now)))
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
		_, err := f.svc.ReprocessDuePending(ctx, sigChain)
		require.NoError(t, err)
	}

	assert.Equal(t, []uint64{990}, read, "only the pending block is read: the pending retry runs, the block scan does not")
	assert.Empty(t, f.pendingBlocks(t))
	checkpoint, err := f.svc.checkpoints.Find(ctx, sigChain)
	require.NoError(t, err)
	assert.Equal(t, uint64(sigCheckpoint), checkpoint)
	assert.Len(t, recordsOf(logs(), "INFO", "deposit block scan paused: the signature sync reads the chain's deposits"), 1, "logged on the change, not every cycle")
	_, err = f.svc.RunOperator(ctx, OperatorScan{ChainID: sigChain})
	require.ErrorIs(t, err, ErrSignatureMode)
	assert.Contains(t, err.Error(), "deposits:sync-signatures "+sigChain)

	f.settings.Chains = nil
	require.NoError(t, f.svc.SetScanOptions(ScanOptions{BatchBlocks: 5, CatchUpBlocks: 5, Concurrency: 1}))
	require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))

	assert.Len(t, read, 1+5, "the block scan resumes from its checkpoint")
	checkpoint, err = f.svc.checkpoints.Find(ctx, sigChain)
	require.NoError(t, err)
	assert.Equal(t, uint64(sigCheckpoint+5), checkpoint)
	assert.Len(t, recordsOf(logs(), "INFO", "deposit block scan resumed: the signature sync is off for the chain"), 1)
}

func TestScan_LatestBlocks_KeepsScanningAChainTheSettingsListButCannotSyncBySignatures(t *testing.T) {
	logs := captureLogs(t)
	f := newSignatureFixture(t)
	plain := newPlainChain(models.ChainETH, 100)
	f.registry.RegisterChain(plain)
	f.settings.Chains = []string{models.ChainETH}

	require.NoError(t, f.svc.ScanLatestBlocks(context.Background(), models.ChainETH))
	require.NoError(t, f.svc.ScanLatestBlocks(context.Background(), models.ChainETH))

	checkpoint, err := f.svc.checkpoints.Find(context.Background(), models.ChainETH)
	require.NoError(t, err, "the first cycle starts the chain at the head")
	assert.Equal(t, uint64(100), checkpoint)
	assert.Len(t, recordsOf(logs(), "WARN", "deposit signature sync enabled for a chain whose adapter cannot read signatures; its block scan keeps running"), 1)
}

// A chain whose scan records the block scan reads it (not listed in
// deposit_signatures), with a block scan stalled for hours, is alerted on as such
// even after a one-off sync: the block scan is its only detector. A chain in
// signature mode alerts on its sync instead, and clears the alerts of its paused scan.
func TestScan_Alerts_FollowTheRecordedModeNotTheNewestRecord(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	// sol: the block scan stalled 3 h ago, a one-off sync ran a minute ago, the scan
	// still reads it.
	activity.scans["sol"] = ScanRecord{Head: 1_000_000, Checkpoint: 775_000, At: activityNow.Add(-3 * time.Hour)}
	activity.signatures["sol"] = SignatureSyncRecord{Accounts: 36, At: activityNow.Add(-time.Minute)}
	activity.modes["sol"] = ScanModeRecord{Mode: ScanModeBlocks, Since: activityNow.Add(-24 * time.Hour), At: activityNow}
	// eth: in signature mode, its sync left an account failed, skipped a mint, and its
	// last cycle ended 30 minutes ago.
	activity.scans["eth"] = ScanRecord{Head: 5000, Checkpoint: 100, At: activityNow.Add(-6 * time.Hour)}
	activity.signatures["eth"] = SignatureSyncRecord{Accounts: 2, FailedAccounts: 1, SkippedMints: []string{"mint-x"}, At: activityNow.Add(-30 * time.Minute)}
	activity.modes["eth"] = ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow.Add(-time.Hour), At: activityNow}
	// btc: just switched to signature mode, no completed sync yet.
	activity.modes["btc"] = ScanModeRecord{Mode: ScanModeSignatures, Since: activityNow.Add(-time.Minute), At: activityNow}
	svc := activityService(activity, nil, clock)
	svc.txRepo = &sweepConfirmMemory{}
	svc.SetScanAlertState(newMemoryAlerts(clock))
	svc.signatureSettings = func(context.Context) SignatureSyncSettings { return SignatureSyncSettings{Interval: 10 * time.Minute} }

	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))

	records := logs()
	assert.Contains(t, warnings(records, scanStaleMessage), "sol", "the stalled block scan is sol's only detector")
	assert.Contains(t, warnings(records, scanLagMessage), "sol", "and it trails the head by 225000 slots")
	assert.NotContains(t, warnings(records, scanStaleMessage), "eth", "a paused block scan raises no alert")
	assert.NotContains(t, warnings(records, scanLagMessage), "eth")
	stale := warnings(records, signatureStaleMessage)
	assert.Contains(t, stale, "eth", "30 minutes is past the stale threshold plus the interval (20 minutes)")
	assert.NotContains(t, stale, "btc", "a sync that just took the chain over is not stale yet")
	assert.NotContains(t, stale, "sol")
	assert.Equal(t, float64(1), warnings(records, signatureFailedMessage)["eth"]["failed_accounts"])
	assert.Contains(t, warnings(records, signatureMintsMessage), "eth")
	readings := svc.ScanActivityHealth(context.Background())
	assert.False(t, readings.Chains["sol"].SignatureMode(), "/health does not report signature mode for an unlisted chain")
	assert.True(t, readings.Chains["eth"].SignatureMode())

	activity.modes["eth"] = ScanModeRecord{Mode: ScanModeBlocks, Since: activityNow, At: activityNow}
	activity.scans["eth"] = ScanRecord{Head: 5000, Checkpoint: 5000, At: activityNow}
	clock.advance(time.Minute)
	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))
	assert.Len(t, recordsOf(logs(), "INFO", signatureStaleClearedMessage), 1, "a chain the block scan reads again clears its sync alerts")
	assert.Len(t, recordsOf(logs(), "INFO", signatureMintsClearedMessage), 1)
}

func TestSync_Signatures_RecordsItsCycleForTheHealthCheck(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.accountErr[sigBob] = fmt.Errorf("429")
	activity := newMemoryActivity()
	f.svc.SetScanActivityStore(activity)
	f.svc.now = func() time.Time { return activityNow }

	f.sync(t)

	record, ok := activity.syncOf(sigChain)
	require.True(t, ok)
	assert.Equal(t, SignatureSyncRecord{Accounts: 3, FailedAccounts: 1, BackfillingAccounts: 2, Head: sigHead, ListedHead: sigHead, BackfillingSince: activityNow, At: activityNow}, record,
		"alice's two accounts, whose end showed once, and bob, whose accounts were not listed; the first cycle that left accounts backfilling")
}

func pendingEntryDue(block uint64, now time.Time) pending.Entry {
	return pending.Entry{Chain: sigChain, Block: block, Attempts: 3, ErrorClass: pending.ClassRPC, LastError: "429",
		FirstFailedAt: now, LastFailedAt: now, NextRetryAt: now}
}

// newPlainChain is a chain whose adapter reads blocks only, its head at head.
func newPlainChain(chainID string, head uint64) *mocks.MockChain {
	adapter := mocks.NewMockChain(chainID)
	adapter.GetLatestBlockFn = func(context.Context) (uint64, error) { return head, nil }
	adapter.ScanBlockFn = func(context.Context, uint64) ([]types.DetectedTransfer, error) { return nil, nil }
	return adapter
}
