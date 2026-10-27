package deposit

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"sync"
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/chain"
)

// A burst of new signatures longer than a cycle lists above an account's cursor
// (signatureForwardPageCeiling pages) is recorded newest first as a stretch of its
// own ahead of the cursor, and the gap below it is read down to the cursor over the
// next cycles while newer signatures keep being recorded above it. The stretches join
// once the gap is read, and only then is the account read up to the head.
func TestSync_Signatures_ReadsABurstLongerThanACycleListsOverSeveralCycles(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.pageSize = 1
	f.chain.accounts[sigAlice] = []string{sigAlice}
	f.chain.history(sigBob, f.chain.tx("sig-0", 1100, lamports(sigBob, 1, 1)))
	f.syncCovered(t)
	const burst = signatureForwardPageCeiling + 2*signatureBackfillPages + 2
	const newest = 1200 + burst
	bobHistory(f, 1201, newest)

	first := f.sync(t)

	assert.Equal(t, signatureForwardPageCeiling+signatureBackfillPages, first.Deposits, "the newest listed, then one batch of the gap below them")
	assert.Zero(t, first.FailedAccounts, "a burst fails nothing")
	assert.Equal(t, 1, first.BackfillingAccounts, "the gap is still read")
	assert.Zero(t, first.CoveredHead)
	cursor := f.cursors(t)[sigBob]
	sameCursor(t, cursor, "sig-0", 1100, "sig-0", sigCheckpoint, true)
	aheadBefore := fmt.Sprintf("sig-%d", newest-signatureForwardPageCeiling-signatureBackfillPages+1)
	assert.Equal(t, fmt.Sprintf("sig-%d", newest), cursor.AheadSignature)
	assert.Equal(t, int64(newest), cursor.AheadSlot)
	assert.Equal(t, aheadBefore, cursor.AheadBefore)

	f.chain.history(sigBob, f.chain.tx("sig-new", 1500, lamports(sigBob, 7, 1)))
	second := f.sync(t)

	assert.Equal(t, 1+signatureBackfillPages, second.Deposits, "the new signature above the burst, the next batch of the gap")
	assert.Zero(t, second.CoveredHead)
	queries := f.chain.queriesOf(sigBob)
	assert.Equal(t, chain.SignatureQuery{Until: fmt.Sprintf("sig-%d", newest), FloorSlot: newest, HasFloor: true, MaxPages: signatureForwardPageCeiling},
		queries[len(queries)-2], "new signatures are read above the stretch ahead of the gap")
	aheadAnchor := fmt.Sprintf("sig-%d", newest-signatureForwardPageCeiling-signatureBackfillPages+2)
	assert.Equal(t, aheadAnchor, cursor.AheadAnchor, "the signature recorded right above the stretch's oldest")
	assert.Equal(t, chain.SignatureQuery{Until: "sig-0", Before: aheadBefore, Anchor: aheadAnchor, FloorSlot: 1100, HasFloor: true, MaxPages: signatureBackfillPages},
		queries[len(queries)-1], "the gap is read below that stretch, from right below its anchor, down to the cursor")
	assert.Equal(t, "sig-new", f.cursors(t)[sigBob].AheadSignature)

	third := f.sync(t)

	assert.Equal(t, 2, third.Deposits, "the end of the gap")
	assert.Equal(t, uint64(sigCovered), third.CoveredHead, "nothing above the cursor is left unread")
	cursor = f.cursors(t)[sigBob]
	sameCursor(t, cursor, "sig-new", 1500, "sig-0", sigCheckpoint, true)
	assert.Empty(t, cursor.AheadSignature, "the two stretches joined")
	assert.Empty(t, cursor.AheadBefore)
	assert.Zero(t, cursor.AheadSlot)
	assert.Len(t, sigDeposits(t), 1+burst+1, "every signature recorded once")
}

// Another burst while the gap of the last one is read starts a new stretch ahead of
// the cursor: the one recorded ahead of that gap becomes part of the larger gap and is
// read again, and the account never fails.
func TestSync_Signatures_ASecondBurstWhileAGapIsReadFailsNothing(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.pageSize = 1
	f.chain.accounts[sigAlice] = []string{sigAlice}
	f.chain.history(sigBob, f.chain.tx("sig-0", 1100, lamports(sigBob, 1, 1)))
	f.syncCovered(t)
	bobHistory(f, 1201, 1310)
	f.sync(t)
	require.Equal(t, "sig-1310", f.cursors(t)[sigBob].AheadSignature)

	bobHistory(f, 1401, 1510)
	second := f.sync(t)

	assert.Zero(t, second.FailedAccounts)
	assert.Equal(t, "sig-1510", f.cursors(t)[sigBob].AheadSignature, "a new stretch ahead, from the newest signature")
	for i := 0; i < 60 && f.cursors(t)[sigBob].AheadSignature != ""; i++ {
		summary := f.sync(t)
		require.Zero(t, summary.FailedAccounts)
	}
	cursor := f.cursors(t)[sigBob]
	assert.Empty(t, cursor.AheadSignature, "the gap is read")
	sameCursor(t, cursor, "sig-1510", 1510, "sig-0", sigCheckpoint, true)
	assert.Len(t, sigDeposits(t), 1+110+110, "every signature recorded once")
}

// An account known empty, read above the slot it is known empty up to from an
// endpoint without the old ledger (its first available block above that slot), gets a
// listing whose end nothing proves: the cursor does not move over it. What the
// endpoint shows is recorded as a stretch ahead of the cursor, and the gap below it
// stays open until an endpoint that holds the ledger shows the deposit it hid.
func TestSync_Signatures_AKnownEmptyAccountsCursorMovesOnlyOnAProvenEnd(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.accounts[sigAlice] = []string{sigAlice}
	f.syncCovered(t)
	sameCursor(t, f.cursors(t)[sigBob], "", sigCovered, "", sigCheckpoint, true)
	older, newer := uint64(sigCovered+50), uint64(sigCovered+110)
	f.chain.history(sigBob,
		f.chain.tx("sig-older", older, lamports(sigBob, 19, 1)),
		f.chain.tx("sig-newer", newer, lamports(sigBob, 20, 1)))
	f.chain.mu.Lock()
	f.chain.heads[sigBob] = sigHead + 100
	f.chain.hideBelow = sigCovered + 100
	f.chain.mu.Unlock()

	partial := f.sync(t)

	cursor := f.cursors(t)[sigBob]
	assert.Empty(t, cursor.Signature, "the cursor does not move over a listing whose end is not proven")
	assert.Equal(t, "sig-newer", cursor.AheadSignature, "what the endpoint showed is recorded ahead of it")
	assert.Equal(t, 2, partial.BackfillingAccounts, "bob's gap is still read, and nothing proves alice's end on this endpoint either")
	assert.Zero(t, partial.CoveredHead)

	f.chain.mu.Lock()
	f.chain.hideBelow = 0
	f.chain.mu.Unlock()
	f.sync(t)

	var got []string
	for _, row := range sigDeposits(t) {
		got = append(got, row.TxHash)
	}
	assert.ElementsMatch(t, []string{"sig-older", "sig-newer"}, got, "the endpoint that holds the ledger shows the hidden deposit")
	sameCursor(t, f.cursors(t)[sigBob], "sig-newer", int64(newer), "sig-older", sigCheckpoint, true)
	assert.Empty(t, f.cursors(t)[sigBob].AheadSignature)
}

// A pending block within the index margin below the head may hold a deposit the
// listing had not served yet (a backend behind on the newest slots): a cycle drops only
// the pending blocks up to the slot every account is read up to, so the block stays
// for the pending retry, and the deposit is recorded by the next forward read or, once
// the chain leaves the list, by the block scan.
func TestSync_Signatures_APendingBlockInsideTheIndexMarginIsNeverLost(t *testing.T) {
	for name, leave := range map[string]bool{"stays listed": false, "leaves the list": true} {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			activity := newMemoryActivity()
			f.svc.SetScanActivityStore(activity)
			ctx := context.Background()
			f.chain.accounts[sigAlice] = []string{sigAlice}
			require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
			f.syncCovered(t)
			near := uint64(sigHead - 10)
			f.chain.history(sigBob, f.chain.tx("sig-near", near, lamports(sigBob, 9, 1)))
			f.keepPending(t, near)
			f.chain.mu.Lock()
			f.chain.hideAbove = near - 10 // this cycle's backend has not served the slot yet
			f.chain.mu.Unlock()

			summary := f.sync(t)

			assert.Equal(t, uint64(sigCovered), summary.CoveredHead)
			assert.Zero(t, summary.ResolvedPending)
			assert.Equal(t, []uint64{near}, f.pendingBlocks(t), "above the covered slot: it stays pending")

			f.chain.mu.Lock()
			f.chain.hideAbove = 0
			f.chain.mu.Unlock()
			if leave {
				f.settings.Chains = nil
				require.NoError(t, f.svc.SetScanOptions(ScanOptions{BatchBlocks: 2000, CatchUpBlocks: 2000, Concurrency: 8}))
				require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
				_, err := f.svc.ReprocessPending(ctx, sigChain, true)
				require.NoError(t, err)
			} else {
				f.sync(t)
			}
			rows := sigDeposits(t)
			require.Len(t, rows, 1)
			assert.Equal(t, "sig-near", rows[0].TxHash)
		})
	}
}

// signatureEndpoints is which endpoints a property run reads from, cycle by cycle.
type signatureEndpoints int

const (
	// mixedEndpoints: a full endpoint, one behind on the newest slots (within the
	// index margin) or one without the old ledger.
	mixedEndpoints signatureEndpoints = iota
	// laggingEndpoints: a full endpoint or one behind on the newest slots.
	laggingEndpoints
	// nonArchivalEndpoints: a full endpoint or one without the old ledger.
	nonArchivalEndpoints
)

type propertySignature struct {
	slot    uint64
	deposit bool
}

// signatureRun is how the endpoint of a property run behaves.
type signatureRun struct {
	// unknownErrs: the endpoint fails a before or until it does not show (Agave)
	// instead of answering what it holds.
	unknownErrs bool
	// switching: each cycle draws whether it fails or answers (a load balancer over
	// both kinds of backend), whatever unknownErrs says.
	switching bool
	// race: an empty first page above a cursor stays unended one time in three, as
	// when signatures land between the page and its confirmations.
	race bool
	// newestOnUnknown: an endpoint answering a before it does not index answers from
	// its own newest signature instead of with an empty page (whatever its status
	// store holds: the sync asks only the index).
	newestOnUnknown bool
	endpoints       signatureEndpoints
}

// runSignatureProperty drives 30 cycles over two accounts with bursts above the
// forward ceiling (also while a gap is open), transient read failures (in a gap too),
// several signatures per slot, failed transactions and an endpoint that changes from
// cycle to cycle (run: a backend behind on the newest slots anywhere within the index
// margin, or one without the old ledger; failing or answering what it does not show;
// confirmation races). After every cycle each
// recorded stretch (the cursor's, the one ahead of a gap) must be fully recorded, and
// a covered account fully recorded from its floor up to its cursor or the slot it is
// known empty up to (violations). Once the endpoint is full and the failures stop,
// every deposit at or above the floor must be recorded (missing).
func runSignatureProperty(t *testing.T, seed int64, run signatureRun) (missing, violations int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	f := newSignatureFixture(t)
	f.chain.pageSize = 1 + rng.Intn(2)
	ceiling := signatureForwardPageCeiling * f.chain.pageSize
	f.chain.accounts[sigAlice] = []string{sigAlice}
	f.chain.newestOnUnknown = run.newestOnUnknown
	if run.race {
		var raceMu sync.Mutex
		f.chain.raceMiss = func() bool {
			raceMu.Lock()
			defer raceMu.Unlock()
			return rng.Intn(3) == 0
		}
	}
	accounts := []string{sigAlice, sigBob}
	order := map[string][]string{}
	known := map[string]propertySignature{}
	head := uint64(1100)
	n := 0
	add := func(account string, count int, from, to uint64) {
		slot := from
		for i := 0; i < count; i++ {
			if slot < to && rng.Intn(3) > 0 {
				slot = min(to, slot+1+draw(rng, 3))
			}
			n++
			signature := fmt.Sprintf("p-%s-%05d-%d", account[:3], slot, n)
			order[account] = append(order[account], signature)
			if rng.Intn(8) == 0 {
				f.chain.history(account, failed(signature, slot))
				known[signature] = propertySignature{slot: slot}
				continue
			}
			f.chain.history(account, f.chain.tx(signature, slot, lamports(account, int64(n), 1)))
			known[signature] = propertySignature{slot: slot, deposit: true}
		}
	}
	for _, account := range accounts {
		add(account, 30+rng.Intn(40), 985, 1090)
	}
	setHead := func() {
		f.chain.mu.Lock()
		for _, account := range accounts {
			f.chain.heads[account] = head
		}
		f.chain.mu.Unlock()
		at := head
		f.chain.GetLatestBlockFn = func(context.Context) (uint64, error) { return at, nil }
	}
	indexOf := func(account, signature string) int {
		for i, listed := range order[account] {
			if listed == signature {
				return i
			}
		}
		return -1
	}
	check := func(stage string) {
		recorded := map[string]bool{}
		for _, row := range sigDeposits(t) {
			recorded[row.TxHash] = true
		}
		for account, cursor := range f.cursors(t) {
			floor := storedSlot(cursor.SyncedFromSlot)
			span := func(low, high int, what string) {
				if low < 0 || high < 0 {
					return
				}
				for i := low; i <= high; i++ {
					signature := order[account][i]
					if e := known[signature]; e.deposit && e.slot >= floor && !recorded[signature] {
						t.Errorf("%s, seed %d: %s's %s holds %s at %d, not recorded", stage, seed, account, what, signature, e.slot)
						violations++
					}
				}
			}
			top := indexOf(account, cursor.Signature)
			span(indexOf(account, cursor.BackfillBefore), top, "stretch")
			span(indexOf(account, cursor.AheadBefore), indexOf(account, cursor.AheadSignature), "stretch ahead of a gap")
			anchored := func(end, anchor, what string) {
				if anchor == "" {
					return
				}
				if at := indexOf(account, end); at < 0 || indexOf(account, anchor) != at+1 {
					t.Errorf("%s, seed %d: %s's %s anchor %s is not the signature right above %s", stage, seed, account, what, anchor, end)
					violations++
				}
			}
			anchored(cursor.BackfillBefore, cursor.BackfillAnchor, "oldest recorded signature's")
			anchored(cursor.AheadBefore, cursor.AheadAnchor, "stretch ahead's oldest signature's")
			if !cursor.Covered {
				continue
			}
			if cursor.Signature != "" {
				span(0, top, "covered history")
				continue
			}
			for _, signature := range order[account] {
				if e := known[signature]; e.deposit && e.slot >= floor && e.slot <= storedSlot(cursor.Slot) && !recorded[signature] {
					t.Errorf("%s, seed %d: %s known empty up to %d, but %s at %d is not recorded", stage, seed, account, cursor.Slot, signature, e.slot)
					violations++
				}
			}
		}
	}
	setHead()
	for cycle := 0; cycle < 30; cycle++ {
		f.chain.mu.Lock()
		f.chain.hideAbove, f.chain.hideBelow, f.chain.unknownErrs = 0, 0, run.unknownErrs
		if run.switching {
			f.chain.unknownErrs = rng.Intn(2) == 0
		}
		pick := rng.Intn(4)
		if run.endpoints == laggingEndpoints && pick == 2 || run.endpoints == nonArchivalEndpoints && pick == 1 {
			pick = 0
		}
		switch pick {
		case 1: // a backend behind on the newest slots, anywhere within the index margin
			f.chain.hideAbove = head - draw(rng, 1400)
		case 2: // a backend without the old ledger
			f.chain.hideBelow = head - 300 - draw(rng, 600)
		}
		f.chain.txErr = map[string]error{}
		for signature, e := range known {
			if e.deposit && rng.Intn(40) == 0 {
				f.chain.txErr[signature] = fmt.Errorf("429")
			}
		}
		f.chain.mu.Unlock()
		if cycle < 20 {
			previous := head
			head += 200 + draw(rng, 300)
			for _, account := range accounts {
				if rng.Intn(4) == 0 {
					add(account, ceiling+1+rng.Intn(ceiling), previous+1, head)
				} else {
					add(account, rng.Intn(5), previous+1, head)
				}
			}
			setHead()
		}
		_, err := f.svc.SyncSignatures(context.Background(), sigChain)
		require.NoError(t, err)
		check(fmt.Sprintf("cycle %d", cycle))
	}
	f.chain.mu.Lock()
	f.chain.hideAbove, f.chain.hideBelow = 0, 0
	f.chain.txErr = map[string]error{}
	f.chain.mu.Unlock()
	for i := 0; i < 800; i++ {
		summary, err := f.svc.SyncSignatures(context.Background(), sigChain)
		require.NoError(t, err)
		if i%10 == 0 {
			check(fmt.Sprintf("settling cycle %d", i))
		}
		if summary.FailedAccounts == 0 && summary.BackfillingAccounts == 0 && summary.CoveredHead != 0 {
			break
		}
	}
	recorded := map[string]bool{}
	for _, row := range sigDeposits(t) {
		recorded[row.TxHash] = true
	}
	for signature, e := range known {
		if e.deposit && e.slot >= sigCheckpoint && !recorded[signature] {
			missing++
			t.Errorf("seed %d: %s at %d never recorded", seed, signature, e.slot)
		}
	}
	for account, cursor := range f.cursors(t) {
		assert.True(t, cursor.Covered, "seed %d: %s covered once the endpoint is full", seed, account)
		assert.Empty(t, cursor.AheadSignature, "seed %d: %s has no gap left", seed, account)
	}
	check("settled")
	return missing, violations
}

// Over cycles with bursts, gaps, failures and endpoints that show less than they hold,
// no signature at or above the floor is skipped and no account is covered early,
// whether the endpoint fails a signature it does not hold (Agave), answers what it
// holds (another provider) or switches between the two from cycle to cycle, with
// lagging or non-archival backends and confirmation races. Each seed records a few
// thousand deposits, so the run keeps a few seeds per kind of endpoint. The seeds of
// the switching runs are the review's (#130): before the adapter confirmed that an
// endpoint holds the before of a listing, seeds 204 and 206 lost hundreds of
// signatures; the races make a run differ from one time to the next, so 206 runs twice.
// The runs of an index behind are the review's of the status check (501 to 505): an
// index that does not hold a before its status store holds lost 3 to 449 deposits in
// four of five seeds before listings were anchored. Each cycle also checks every
// stored anchor is the signature right above its end.
func TestProperty_SignatureSync_ReadsEveryHistoryWhateverTheEndpointShows(t *testing.T) {
	for name, tc := range map[string]struct {
		run   signatureRun
		seeds []int64
	}{
		"failing an unknown signature, mixed backends":   {run: signatureRun{unknownErrs: true}, seeds: seedRange(1, 4)},
		"answering an unknown signature, mixed backends": {run: signatureRun{}, seeds: seedRange(1, 4)},
		"answering, lagging backends":                    {run: signatureRun{endpoints: laggingEndpoints}, seeds: seedRange(1, 2)},
		"answering, non-archival backends":               {run: signatureRun{endpoints: nonArchivalEndpoints}, seeds: seedRange(1, 2)},
		"switching, mixed backends":                      {run: signatureRun{switching: true}, seeds: seedRange(101, 103)},
		"switching, with confirmation races":             {run: signatureRun{switching: true, race: true}, seeds: append(seedRange(201, 206), 206)},
		"failing, with confirmation races":               {run: signatureRun{unknownErrs: true, race: true}, seeds: seedRange(301, 301)},
		// The review's index behind its status store (#130 follow-up): a lagging index
		// that answers a before it does not hold with an empty page, or from its newest.
		"an index behind, answering empty":               {run: signatureRun{endpoints: laggingEndpoints}, seeds: seedRange(501, 506)},
		"an index behind, answering from its newest":     {run: signatureRun{endpoints: laggingEndpoints, newestOnUnknown: true}, seeds: seedRange(501, 506)},
		"answering from its newest, mixed backends":      {run: signatureRun{newestOnUnknown: true}, seeds: seedRange(601, 602)},
		"switching, from its newest, confirmation races": {run: signatureRun{switching: true, race: true, newestOnUnknown: true}, seeds: seedRange(701, 702)},
	} {
		t.Run(name, func(t *testing.T) {
			for i, seed := range tc.seeds {
				t.Run(fmt.Sprintf("%d#%d", seed, i), func(t *testing.T) {
					skipOutsidePropertyShard(t, fmt.Sprintf("%s/%d#%d", name, seed, i))
					missing, violations := runSignatureProperty(t, seed, tc.run)
					assert.Zero(t, missing, "deposits never recorded")
					assert.Zero(t, violations, "stretches or coverage claimed over unrecorded deposits")
				})
			}
		})
	}
}

// skipOutsidePropertyShard skips a property case outside SIGNATURE_PROPERTY_SHARD=k/n
// (1 <= k <= n), so CI runs the property test in n jobs; each case falls in one shard
// by the hash of its name. Unset runs every case.
func skipOutsidePropertyShard(t *testing.T, caseName string) {
	t.Helper()
	raw := os.Getenv("SIGNATURE_PROPERTY_SHARD")
	if raw == "" {
		return
	}
	var shard, total int
	if _, err := fmt.Sscanf(raw, "%d/%d", &shard, &total); err != nil || total < 1 || shard < 1 || shard > total {
		t.Fatalf("SIGNATURE_PROPERTY_SHARD=%q: want k/n with 1 <= k <= n", raw)
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(caseName))
	if int(hash.Sum32()%uint32(total)) != shard-1 { //nolint:gosec // G115: total is a small positive shard count
		t.Skipf("case in another shard of %d", total)
	}
}

// draw is rng.Intn(n) as a count of slots, the review's draws (#130) kept so its seeds
// replay here.
func draw(rng *rand.Rand, n int) uint64 {
	return uint64(rng.Intn(n)) //nolint:gosec // G115: Intn answers in [0, n), never a negative value
}

// seedRange is the seeds from first to last.
func seedRange(first, last int64) []int64 {
	seeds := make([]int64, 0, last-first+1)
	for seed := first; seed <= last; seed++ {
		seeds = append(seeds, seed)
	}
	return seeds
}

// A backend behind on the newest slots that answers an empty page for a before it does
// not hold, instead of failing it as Agave does (-32020), shows the end of an account's
// history below a before it cannot place. The adapter confirms the endpoint holds the
// before first, so two cycles on that backend never cover the account with its older
// history unread; once a full endpoint serves, every deposit above the floor is read.
func TestSync_Signatures_ABackendThatDoesNotHoldTheBeforeNeverCoversAnUnreadHistory(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.pageSize = 1
	f.chain.accounts[sigAlice] = []string{sigAlice}
	for slot := uint64(1001); slot <= 1010; slot++ {
		f.chain.history(sigBob, f.chain.tx(fmt.Sprintf("old-%d", slot), slot, lamports(sigBob, int64(slot), 1)))
	}
	for slot := uint64(4900); slot <= 4909; slot++ {
		f.chain.history(sigBob, f.chain.tx(fmt.Sprintf("new-%d", slot), slot, lamports(sigBob, int64(slot), 1)))
	}
	f.sync(t) // the five newest pages of one signature: bob's oldest recorded is new-4905
	require.Equal(t, "new-4905", f.cursors(t)[sigBob].BackfillBefore)
	f.chain.mu.Lock()
	f.chain.hideAbove = 4800 // a backend 100 slots behind, answering an unknown before with an empty page
	f.chain.mu.Unlock()
	f.sync(t)
	f.sync(t)
	cursor := f.cursors(t)[sigBob]
	assert.False(t, cursor.Covered, "an empty page below a before the endpoint does not hold proves no end")
	assert.Equal(t, "new-4905", cursor.BackfillBefore)

	f.chain.mu.Lock()
	f.chain.hideAbove = 0
	f.chain.mu.Unlock()
	for i := 0; i < 5; i++ {
		f.sync(t)
	}
	recorded := map[string]bool{}
	for _, row := range sigDeposits(t) {
		recorded[row.TxHash] = true
	}
	var missing []string
	for slot := uint64(1001); slot <= 1010; slot++ {
		if !recorded[fmt.Sprintf("old-%d", slot)] {
			missing = append(missing, fmt.Sprintf("old-%d", slot))
		}
	}
	assert.Empty(t, missing, "bob's history above the floor is recorded")
	assert.True(t, f.cursors(t)[sigBob].Covered)
}

// An address index behind on the newest slots (its status store may hold more) that
// answers a before it does not hold with an empty page, or from its own newest
// signatures, instead of failing it: before listings were anchored, the empty answer
// covered bob with 25 of his 30 deposits unread and the newest one skipped 5 (the
// review of #130). Every listing below an end now starts right below its anchor and
// needs the end first, so that backend reads nothing below it, and once a full index
// serves every deposit above the floor is recorded.
func TestSync_Signatures_AnIndexBehindItsStatusStoreNeverSkipsAnUnreadHistory(t *testing.T) {
	for name, newest := range map[string]bool{"an empty page": false, "its own newest signatures": true} {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			f.chain.pageSize = 1
			f.chain.accounts[sigAlice] = []string{sigAlice}
			var want []string
			for _, run := range []struct {
				prefix   string
				from, to uint64
			}{{"old", 1001, 1010}, {"mid", 4790, 4799}, {"new", 4900, 4909}} {
				for slot := run.from; slot <= run.to; slot++ {
					signature := fmt.Sprintf("%s-%d", run.prefix, slot)
					f.chain.history(sigBob, f.chain.tx(signature, slot, lamports(sigBob, 1_000+int64(len(want)), 1)))
					want = append(want, signature)
				}
			}
			f.sync(t) // the five newest pages of one signature
			first := f.cursors(t)[sigBob]
			require.Equal(t, "new-4905", first.BackfillBefore)
			require.Equal(t, "new-4906", first.BackfillAnchor, "the signature recorded right above the oldest")

			f.chain.mu.Lock()
			f.chain.hideAbove, f.chain.newestOnUnknown = 4800, newest // an index 100 slots behind
			f.chain.mu.Unlock()
			f.sync(t)
			f.sync(t)
			cursor := f.cursors(t)[sigBob]
			assert.False(t, cursor.Covered, "nothing below an end the index does not place proves an end")
			assert.Equal(t, "new-4905", cursor.BackfillBefore, "and nothing moves the oldest recorded signature down")

			f.chain.mu.Lock()
			f.chain.hideAbove, f.chain.newestOnUnknown = 0, false
			f.chain.mu.Unlock()
			for i := 0; i < 8; i++ {
				f.sync(t)
			}
			recorded := map[string]bool{}
			for _, row := range sigDeposits(t) {
				recorded[row.TxHash] = true
			}
			var missing []string
			for _, signature := range want {
				if !recorded[signature] {
					missing = append(missing, signature)
				}
			}
			assert.Empty(t, missing, "bob's 30 deposits above the floor are recorded")
			assert.True(t, f.cursors(t)[sigBob].Covered)
		})
	}
}

// A cursor saved before anchors were recorded holds a stretch of several signatures
// with no anchor on its ends: its oldest recorded signature is first moved up to its
// newest, the history below is read again (recording is idempotent), and the anchors
// are known from then on; nothing is lost and no deposit is recorded twice.
func TestSync_Signatures_ACursorSavedWithoutAnchorsIsReadAgainFromItsNewestSignature(t *testing.T) {
	logs := captureLogs(t)
	f := newSignatureFixture(t)
	f.chain.pageSize = 1
	f.chain.accounts[sigAlice] = []string{sigAlice}
	for slot := uint64(1001); slot <= 1012; slot++ {
		f.chain.history(sigBob, f.chain.tx(fmt.Sprintf("sig-%d", slot), slot, lamports(sigBob, int64(slot), 1)))
	}
	f.sync(t)
	cursor := f.cursors(t)[sigBob]
	require.Equal(t, "sig-1008", cursor.BackfillBefore)
	_, err := facades.Orm().Query().Exec(`UPDATE deposit_signature_cursors SET backfill_anchor = '' WHERE chain = ? AND account = ?`, sigChain, sigBob)
	require.NoError(t, err)

	f.sync(t)
	assert.Len(t, recordsOf(logs(), "INFO", "deposit signature sync: an account's oldest recorded signature has no anchor; its history is read again from its newest recorded signature"), 1)
	reread := f.cursors(t)[sigBob]
	assert.Equal(t, "sig-1007", reread.BackfillBefore, "read again from the newest: five pages of one signature below it")
	assert.Equal(t, "sig-1008", reread.BackfillAnchor)

	for i := 0; i < 4; i++ {
		f.sync(t)
	}
	rows := sigDeposits(t)
	assert.Len(t, rows, 12, "every deposit once")
	assert.True(t, f.cursors(t)[sigBob].Covered)
}
