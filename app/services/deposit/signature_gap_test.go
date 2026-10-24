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

// Over many cycles with page boundaries, several signatures per slot, failed
// transactions, transient read errors, new arrivals and a burst longer than a cycle
// lists, no signature at or above the floor is skipped: each recorded stretch (the
// cursor's, and the one ahead of a gap) is fully recorded, an account is covered only
// when everything from its floor up to its cursor is, and once the errors stop every
// deposit is recorded and every account covered.
func TestSync_Signatures_ReadsEveryHistoryAcrossCyclesWithoutASkipOrAnEarlyCoverage(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			f := newSignatureFixture(t)
			f.chain.pageSize = 1 + rng.Intn(3)
			f.chain.accounts[sigAlice] = []string{sigAlice}
			type entry struct {
				slot     uint64
				deposit  bool
				expected bool
			}
			known := map[string]map[string]entry{sigAlice: {}, sigBob: {}}
			slots := map[string]uint64{sigAlice: 985, sigBob: 985}
			n := 0
			add := func(account string, count int) {
				for i := 0; i < count; i++ {
					if rng.Intn(3) > 0 {
						slots[account]++
					}
					slot := slots[account]
					n++
					signature := fmt.Sprintf("s-%s-%04d-%d", account[:3], slot, n)
					if rng.Intn(6) == 0 {
						f.chain.history(account, failed(signature, slot))
						known[account][signature] = entry{slot: slot}
						continue
					}
					f.chain.history(account, f.chain.tx(signature, slot, lamports(account, int64(n), 1)))
					known[account][signature] = entry{slot: slot, deposit: true, expected: slot >= sigCheckpoint}
				}
			}
			// stretchRecorded checks that every deposit of account from slot from up to the
			// slot of high, at or above the floor, is recorded; those that share a slot with
			// either end of the stretch (low, high) may lie outside it.
			stretchRecorded := func(stage, account string, from uint64, low, high string, recorded map[string]bool) {
				lowSlot, highSlot := known[account][low].slot, known[account][high].slot
				for signature, e := range known[account] {
					if !e.deposit || e.slot < from || e.slot > highSlot || e.slot < sigCheckpoint || recorded[signature] {
						continue
					}
					if e.slot == lowSlot || e.slot == highSlot {
						continue
					}
					t.Errorf("%s: %s stretch [%d, %d] but %s at %d is not recorded", stage, account, from, highSlot, signature, e.slot)
				}
			}
			check := func(stage string) {
				recorded := map[string]bool{}
				for _, row := range sigDeposits(t) {
					recorded[row.TxHash] = true
				}
				for account, cursor := range f.cursors(t) {
					if cursor.Signature != "" {
						from := known[account][cursor.BackfillBefore].slot
						if cursor.Covered {
							from = storedSlot(cursor.SyncedFromSlot)
						}
						stretchRecorded(stage, account, from, cursor.BackfillBefore, cursor.Signature, recorded)
					}
					if cursor.AheadSignature != "" {
						stretchRecorded(stage+" ahead", account, known[account][cursor.AheadBefore].slot, cursor.AheadBefore, cursor.AheadSignature, recorded)
					}
				}
			}
			add(sigAlice, 40+rng.Intn(30))
			add(sigBob, 40+rng.Intn(30))
			for cycle := 0; cycle < 25; cycle++ {
				f.chain.mu.Lock()
				f.chain.txErr = map[string]error{}
				for account := range known {
					for signature, e := range known[account] {
						if e.deposit && rng.Intn(25) == 0 {
							f.chain.txErr[signature] = fmt.Errorf("429")
						}
					}
				}
				f.chain.mu.Unlock()
				if cycle < 15 && rng.Intn(2) == 0 {
					add(sigAlice, rng.Intn(4))
					add(sigBob, rng.Intn(4))
				}
				if cycle == 6 {
					add(sigBob, signatureForwardPageCeiling*f.chain.pageSize+1+rng.Intn(20))
				}
				_, err := f.svc.SyncSignatures(context.Background(), sigChain)
				require.NoError(t, err)
				check(fmt.Sprintf("cycle %d", cycle))
			}
			f.chain.mu.Lock()
			f.chain.txErr = map[string]error{}
			f.chain.mu.Unlock()
			for i := 0; i < 80; i++ {
				summary, err := f.svc.SyncSignatures(context.Background(), sigChain)
				require.NoError(t, err)
				if summary.FailedAccounts == 0 && summary.BackfillingAccounts == 0 {
					break
				}
			}
			recorded := map[string]bool{}
			for _, row := range sigDeposits(t) {
				recorded[row.TxHash] = true
			}
			for account := range known {
				for signature, e := range known[account] {
					if e.expected && !recorded[signature] {
						t.Errorf("never recorded: %s %s at slot %d", account, signature, e.slot)
					}
				}
			}
			for account, cursor := range f.cursors(t) {
				assert.True(t, cursor.Covered, "%s covered at the end", account)
				assert.Empty(t, cursor.AheadSignature, "%s has no gap left", account)
			}
		})
	}
}
