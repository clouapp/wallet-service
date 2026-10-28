package deposit

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/solanacredit"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const (
	sigCarol = "carol-owner"
	sigDave  = "dave-owner"
)

// watch adds owners to the watched addresses of the fixture's chain, beside alice and
// bob (the base addresses InsertWallet creates stay unwatched).
func (f *signatureFixture) watch(t *testing.T, owners ...string) {
	t.Helper()
	for _, owner := range owners {
		wallet := fixtures.InsertWallet(t, sigChain)
		fixtures.InsertAddress(t, wallet.ID, sigChain, owner, "user-"+owner, 0)
		_, err := facades.Orm().Query().Exec(`UPDATE addresses SET is_active = false WHERE wallet_id = ? AND address <> ?`, wallet.ID, owner)
		require.NoError(t, err)
	}
}

// A chain with more owners than one cycle has time for is read in turns: a listing is
// part of an owner's read, so no owner is listed once the cycle's budget is spent; the
// owners a cycle left out, or cut short, come first in the next one; and the covered
// slot comes once every account was read, by whichever cycles read it, although no
// single cycle read them all.
func TestSync_Signatures_ReadsMoreOwnersThanOneCycleHasTimeForInTurns(t *testing.T) {
	f := newSignatureFixture(t)
	f.watch(t, sigCarol, sigDave)
	f.settings.Concurrency = 1
	clock := &stepClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	f.svc.now = clock.Now
	// Each listing waits on a rate-limited provider: one owner is listed and read
	// within a cycle's budget, the next one only listed.
	f.chain.listDelay, f.chain.listClock = signatureCycleBudget*5/8, clock

	var cycles [][]string
	var covered SignatureSyncSummary
	for cycle := 0; cycle < 12 && covered.CoveredHead == 0; cycle++ {
		before := len(f.chain.listedOwners())
		summary := f.sync(t)
		cycles = append(cycles, f.chain.listedOwners()[before:])
		assert.Zero(t, summary.FailedAccounts, "cycle %d: an owner the budget left out is no failure", cycle)
		assert.Equal(t, 5, summary.Accounts, "cycle %d: every known account counts, read this cycle or not", cycle)
		clock.advance(time.Minute)
		covered = summary
	}

	require.GreaterOrEqual(t, len(cycles), 3)
	for i, listed := range cycles {
		assert.LessOrEqual(t, len(listed), 2, "cycle %d: no owner is listed once the budget is spent: %v", i, listed)
	}
	assert.Equal(t, []string{sigAlice, sigBob}, cycles[0], "never-read owners in address order; bob is listed, not read")
	assert.Equal(t, []string{sigBob, sigCarol}, cycles[1], "bob, cut short, comes first")
	assert.Equal(t, []string{sigCarol, sigDave}, cycles[2], "then the owners never read, before alice")
	assert.Equal(t, uint64(sigCovered), covered.CoveredHead, "every account read up to the head, over several cycles")
	assert.Zero(t, covered.BackfillingAccounts)
}

// An account read up to a head stays read up to it whatever later cycles do, but the
// chain's covered slot waits while the last read of any account did not settle it.
func TestSync_Signatures_ACoveredSlotWaitsForAnAccountWhoseLastReadFailed(t *testing.T) {
	f := newSignatureFixture(t)
	first := f.syncCovered(t)
	require.Equal(t, uint64(sigCovered), first.CoveredHead)

	f.chain.mu.Lock()
	f.chain.accountErr[sigBob] = assert.AnError
	f.chain.mu.Unlock()
	failing := f.sync(t)
	assert.Equal(t, 1, failing.FailedAccounts)
	assert.Zero(t, failing.CoveredHead, "a cycle that failed an owner claims no covered slot")

	f.chain.mu.Lock()
	delete(f.chain.accountErr, sigBob)
	f.chain.mu.Unlock()
	again := f.sync(t)
	assert.Zero(t, again.FailedAccounts)
	assert.Equal(t, uint64(sigCovered), again.CoveredHead, "bob read again settles the chain")
}

// An address receives nothing before it exists: the accounts of an owner created
// after the chain's floor are read down to the lowest slot it can have been created
// at (the head less the most slots produced since), not down to the chain's floor.
func TestSync_Signatures_ReadsANewAddressOnlyDownToTheSlotItWasCreatedAt(t *testing.T) {
	f := newSignatureFixture(t)
	f.watch(t, sigCarol)
	_, err := facades.Orm().Query().Exec(`UPDATE addresses SET created_at = now() - interval '10 minutes 30 seconds' WHERE address = ?`, sigCarol)
	require.NoError(t, err)
	// Alice and bob date from before the chain's floor.
	_, err = facades.Orm().Query().Exec(`UPDATE addresses SET created_at = now() - interval '1 year' WHERE address IN (?, ?)`, sigAlice, sigBob)
	require.NoError(t, err)
	// One slot a minute, the whole history in a day: carol was created at most 10
	// slots under the head, alice and bob before slot 0.
	f.chain.slotsWithin = func(d time.Duration) uint64 {
		if d > 24*time.Hour {
			return sigHead
		}
		return uint64(d / time.Minute) //nolint:gosec // G115: an age, never negative here
	}
	f.chain.history(sigCarol,
		f.chain.tx("sig-before-carol", 2000, lamports(sigCarol, 10, 1)),
		f.chain.tx("sig-carol", sigHead-5, lamports(sigCarol, 20, 1)))

	f.sync(t)

	cursors := f.cursors(t)
	assert.Equal(t, int64(sigHead-10), cursors[sigCarol].SyncedFromSlot, "carol's floor is her creation")
	assert.True(t, cursors[sigCarol].Covered, "the history crossed her floor")
	assert.Equal(t, int64(sigCheckpoint), cursors[sigBob].SyncedFromSlot, "an older owner keeps the chain's floor")
	var carol []string
	for _, row := range sigDeposits(t) {
		if row.ToAddress == sigCarol {
			carol = append(carol, row.TxHash)
		}
	}
	assert.Equal(t, []string{"sig-carol"}, carol)
}

// What the cycles read is kept per process: a service that has not read every
// account yet claims no covered slot, whatever another process covered, and gets one
// once its own cycles read them all, even across cycles.
func TestSync_Signatures_ANewProcessClaimsNoCoverageBeforeItReadEveryAccount(t *testing.T) {
	f := newSignatureFixture(t)
	f.syncCovered(t)

	restarted := newSignatureDepositSvc(f.registry, newWebhookSvc(), func(context.Context) SignatureSyncSettings { return f.settings })
	restarted.SetPendingStore(f.pending)
	restarted.SetDepositEvents(f.events)
	clock := &stepClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	restarted.now = clock.Now
	f.settings.Concurrency = 1
	f.chain.listDelay, f.chain.listClock = signatureCycleBudget*5/8, clock

	first, err := restarted.SyncSignatures(context.Background(), sigChain)
	require.NoError(t, err)
	assert.Zero(t, first.CoveredHead, "bob is listed but not read by this process yet")
	assert.Equal(t, 1, first.BackfillingAccounts)

	clock.advance(time.Minute)
	second, err := restarted.SyncSignatures(context.Background(), sigChain)
	require.NoError(t, err)
	assert.Equal(t, uint64(sigCovered), second.CoveredHead, "bob read by the second cycle, alice by the first")
}

// Every registered token is read the same way: an owner's accounts are the owner and
// its account of each token, read in turns with the other owners, and each credit is
// recorded with its token's symbol and contract at its own position, beside the
// lamports the owner received.
func TestSync_Signatures_RecordsTheCreditsOfEveryRegisteredTokenReadInTurns(t *testing.T) {
	f := newSignatureFixture(t)
	f.watch(t, sigCarol)
	usdt := types.Token{Symbol: models.SymbolUSDT, Contract: "usdt-mint", Decimals: 6, ChainID: sigChain}
	bonk := types.Token{Symbol: "BONK", Contract: "bonk-mint", Decimals: 5, ChainID: sigChain}
	f.registry.RegisterToken(usdt)
	f.registry.RegisterToken(bonk)
	credit := func(token types.Token, owner string, amount int64, accountIndex int) types.DetectedTransfer {
		return types.DetectedTransfer{From: sigPayer, To: owner, Amount: big.NewInt(amount), Asset: token.Symbol, Token: &token,
			LogIndex: solanacredit.TokenPosition(accountIndex)}
	}
	carolAccounts := []string{sigCarol, "carol-usdc", "carol-usdt", "carol-bonk"}
	f.chain.accounts[sigCarol] = carolAccounts
	f.chain.history(sigCarol, f.chain.tx("sig-carol-sol", 1500, lamports(sigCarol, 10_000_000, 1)))
	f.chain.history("carol-usdc", f.chain.tx("sig-carol-usdc", 1501, credit(sigUSDC, sigCarol, 1_000_000, 2)))
	f.chain.history("carol-usdt", f.chain.tx("sig-carol-usdt", 1502, credit(usdt, sigCarol, 2_000_000, 2)))
	// One transaction pays carol two tokens: read once, two deposits at two positions.
	both := f.chain.tx("sig-carol-two", 1503, credit(bonk, sigCarol, 500_000, 2), credit(usdt, sigCarol, 3_000_000, 3))
	f.chain.history("carol-bonk", both)
	f.chain.history("carol-usdt", both)

	f.settings.Concurrency = 1
	clock := &stepClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	f.svc.now = clock.Now
	f.chain.listDelay, f.chain.listClock = signatureCycleBudget*5/8, clock
	var covered SignatureSyncSummary
	for cycle := 0; cycle < 12 && covered.CoveredHead == 0; cycle++ {
		covered = f.sync(t)
		clock.advance(time.Minute)
	}
	require.Equal(t, uint64(sigCovered), covered.CoveredHead, "every account of every owner read")

	type deposit struct {
		hash, asset, contract, amount string
		position                      int
	}
	var got []deposit
	for _, row := range sigDeposits(t) {
		if row.ToAddress == sigCarol {
			got = append(got, deposit{row.TxHash, row.Asset, row.TokenContract, row.Amount, row.LogIndex})
		}
	}
	assert.ElementsMatch(t, []deposit{
		{"sig-carol-sol", models.NativeSOL, "", "10000000", 1},
		{"sig-carol-usdc", models.SymbolUSDC, sigUSDCMint, "1000000", solanacredit.TokenPosition(2)},
		{"sig-carol-usdt", models.SymbolUSDT, "usdt-mint", "2000000", solanacredit.TokenPosition(2)},
		{"sig-carol-two", "BONK", "bonk-mint", "500000", solanacredit.TokenPosition(2)},
		{"sig-carol-two", models.SymbolUSDT, "usdt-mint", "3000000", solanacredit.TokenPosition(3)},
	}, got)
	assert.Equal(t, 1, f.chain.reads("sig-carol-two"), "a transaction two token accounts list is read once")
}
