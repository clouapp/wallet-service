package deposit

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

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
	assert.Equal(t, chain.SignatureQuery{Until: "sig-0", Before: aheadBefore, FloorSlot: 1100, HasFloor: true, MaxPages: signatureBackfillPages},
		queries[len(queries)-1], "the gap is read below that stretch, down to the cursor")
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

// runSignatureProperty drives 30 cycles over two accounts with bursts above the
// forward ceiling (also while a gap is open), transient read failures (in a gap too),
// several signatures per slot, failed transactions and an endpoint that changes from
// cycle to cycle (endpoints); unknownErrs is an endpoint that fails a before or until
// it does not show (Agave) instead of answering what it holds. After every cycle each
// recorded stretch (the cursor's, the one ahead of a gap) must be fully recorded, and
// a covered account fully recorded from its floor up to its cursor or the slot it is
// known empty up to (violations). Once the endpoint is full and the failures stop,
// every deposit at or above the floor must be recorded (missing).
func runSignatureProperty(t *testing.T, seed int64, unknownErrs bool, endpoints signatureEndpoints) (missing, violations int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	f := newSignatureFixture(t)
	f.chain.pageSize = 1 + rng.Intn(2)
	ceiling := signatureForwardPageCeiling * f.chain.pageSize
	f.chain.accounts[sigAlice] = []string{sigAlice}
	accounts := []string{sigAlice, sigBob}
	order := map[string][]string{}
	known := map[string]propertySignature{}
	head := uint64(1100)
	n := 0
	add := func(account string, count int, from, to uint64) {
		slot := from
		for i := 0; i < count; i++ {
			if slot < to && rng.Intn(3) > 0 {
				slot = min(to, slot+1+rng.Uint64()%3)
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
		f.chain.hideAbove, f.chain.hideBelow, f.chain.unknownErrs = 0, 0, unknownErrs
		pick := rng.Intn(4)
		if endpoints == laggingEndpoints && pick == 2 || endpoints == nonArchivalEndpoints && pick == 1 {
			pick = 0
		}
		switch pick {
		case 1: // a backend behind on the newest slots, within the index margin
			f.chain.hideAbove = head - rng.Uint64()%140
		case 2: // a backend without the old ledger
			f.chain.hideBelow = head - 300 - rng.Uint64()%600
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
			head += 200 + rng.Uint64()%300
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
// whether the endpoint fails a signature it does not hold (Agave) or answers what it
// holds (another provider), with lagging or non-archival backends. Each seed records a
// few thousand deposits, so the run keeps a few seeds per kind of endpoint (the review
// ran 8, 8, 4 and 4).
func TestProperty_SignatureSync_ReadsEveryHistoryWhateverTheEndpointShows(t *testing.T) {
	for name, tc := range map[string]struct {
		unknownErrs bool
		endpoints   signatureEndpoints
		seeds       int64
	}{
		"failing an unknown signature, mixed backends":   {unknownErrs: true, endpoints: mixedEndpoints, seeds: 4},
		"answering an unknown signature, mixed backends": {endpoints: mixedEndpoints, seeds: 4},
		"answering, lagging backends":                    {endpoints: laggingEndpoints, seeds: 2},
		"answering, non-archival backends":               {endpoints: nonArchivalEndpoints, seeds: 2},
	} {
		t.Run(name, func(t *testing.T) {
			for seed := int64(1); seed <= tc.seeds; seed++ {
				t.Run(fmt.Sprint(seed), func(t *testing.T) {
					missing, violations := runSignatureProperty(t, seed, tc.unknownErrs, tc.endpoints)
					assert.Zero(t, missing, "deposits never recorded")
					assert.Zero(t, violations, "stretches or coverage claimed over unrecorded deposits")
				})
			}
		})
	}
}
