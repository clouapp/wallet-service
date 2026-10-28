package deposit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// stateChain is the signature sync fake that also reads account states in batches,
// as the Solana adapter does: an account's state is how many transactions its
// history holds (empty for none, an account that does not exist), so every transfer
// moves it, read at the chain's head; states overrides an account's state (a balance
// a deposit and a transfer out left as it was). Its derived accounts are the owner's
// listed ones.
type stateChain struct {
	*signatureChain
	states    map[string]string
	statesErr error
	batches   [][]string
}

func (c *stateChain) DerivedAccounts(_ context.Context, owner string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if accounts, ok := c.accounts[owner]; ok {
		return slices.Clone(accounts), nil
	}
	return []string{owner}, nil
}

func (c *stateChain) AccountStates(ctx context.Context, accounts []string) (chain.AccountStates, error) {
	slot, err := c.GetLatestBlock(ctx)
	if err != nil {
		return chain.AccountStates{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batches = append(c.batches, slices.Clone(accounts))
	if c.statesErr != nil {
		return chain.AccountStates{}, c.statesErr
	}
	if len(accounts) > chain.AccountStateBatch {
		return chain.AccountStates{}, fmt.Errorf("%d accounts in one batch", len(accounts))
	}
	read := chain.AccountStates{Slot: slot, States: make(map[string]string, len(accounts))}
	for _, account := range accounts {
		state := ""
		if n := len(c.histories[account]); n > 0 {
			state = fmt.Sprintf("txs:%d", n)
		}
		if override, ok := c.states[account]; ok {
			state = override
		}
		read.States[account] = state
	}
	return read, nil
}

// withStates makes the fixture's chain read account states.
func (f *signatureFixture) withStates() *stateChain {
	states := &stateChain{signatureChain: f.chain, states: map[string]string{}}
	f.registry.RegisterChain(states)
	return states
}

// oneOwnerACycle makes each cycle read one owner (and list the next): every listing
// spends five eighths of the cycle's budget on the service's clock.
func (f *signatureFixture) oneOwnerACycle() *stepClock {
	f.settings.Concurrency = 1
	clock := &stepClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	f.svc.now = clock.Now
	f.chain.listDelay, f.chain.listClock = signatureBudgetOf(f.settings.Interval)*5/8, clock
	return clock
}

// syncListing runs one cycle and returns it with the owners it listed, in order.
func (f *signatureFixture) syncListing(t *testing.T, clock *stepClock) (SignatureSyncSummary, []string) {
	t.Helper()
	before := len(f.chain.listedOwners())
	summary := f.sync(t)
	clock.advance(time.Minute)
	return summary, f.chain.listedOwners()[before:]
}

func sigDepositHashes(t *testing.T, to string) []string {
	t.Helper()
	var hashes []string
	for _, row := range sigDeposits(t) {
		if row.ToAddress == to {
			hashes = append(hashes, row.TxHash)
		}
	}
	return hashes
}

// A deposit to an address the rotation would reach last is read on the next cycle:
// its account's state moved, so its address comes before every other.
func TestSync_Signatures_ReadsAnAddressWhoseAccountMovedBeforeTheRotation(t *testing.T) {
	f := newSignatureFixture(t)
	f.watch(t, sigCarol, sigDave)
	f.withStates()
	clock := f.oneOwnerACycle()
	for range 4 {
		f.syncListing(t, clock) // alice, bob, carol, dave: each read once, nothing moved
	}

	f.chain.history(sigDave, f.chain.tx("sig-dave", 1500, lamports(sigDave, 10_000_000, 1)))
	summary, listed := f.syncListing(t, clock)

	assert.Equal(t, sigDave, listed[0], "the address that moved comes first, whatever its turn")
	assert.Equal(t, 1, summary.MovedAccounts)
	assert.Equal(t, []string{"sig-dave"}, sigDepositHashes(t, sigDave))
}

// The state an account moved to is stored only once a read proves its newest
// signatures read past the slot that state was first seen at (an address index may
// trail its head by the index margin): until then the address is read first on every
// cycle, so a deposit the index serves late is still read at once.
func TestSync_Signatures_StoresAMovedStateOnlyOnceAReadProvesItPastTheSlotItWasSeenAt(t *testing.T) {
	f := newSignatureFixture(t)
	f.withStates()
	f.chain.history(sigBob, f.chain.tx("sig-bob", 1500, lamports(sigBob, 10, 1)))

	first := f.sync(t)
	assert.Equal(t, 1, first.MovedAccounts)
	assert.Empty(t, f.cursors(t)[sigBob].AccountState, "read up to the head less the margin, not past the slot the state was seen at")

	second := f.sync(t)
	assert.Equal(t, 1, second.MovedAccounts, "still moved: read first again")

	f.chain.mu.Lock()
	f.chain.heads[sigBob] = sigHead + signatureIndexMargin
	f.chain.mu.Unlock()
	f.sync(t)
	stored := f.cursors(t)[sigBob]
	assert.Equal(t, "txs:1", stored.AccountState, "a listing that ended a margin past the slot proves it")
	assert.EqualValues(t, sigHead, stored.AccountStateSlot, "the slot it was first seen at")

	settled := f.sync(t)
	assert.Zero(t, settled.MovedAccounts)
	assert.Equal(t, []string{"sig-bob"}, sigDepositHashes(t, sigBob))
}

// A transfer out offset by a deposit of the same amount leaves an account's balance,
// and so its state, as it was: an address that sent a withdrawal, a sweep or a gas
// seed since the last cycle is read first all the same.
func TestSync_Signatures_ReadsFirstAnAddressThatSentATransferEvenWhenItsStateDidNotMove(t *testing.T) {
	f := newSignatureFixture(t)
	f.watch(t, sigCarol, sigDave)
	states := f.withStates()
	clock := f.oneOwnerACycle()
	for range 4 {
		f.syncListing(t, clock)
	}

	f.chain.history(sigCarol, f.chain.tx("sig-carol", 1600, lamports(sigCarol, 7, 1)))
	states.states[sigCarol] = "" // what carol's state was: the deposit and the sweep offset each other
	wallet := fixtures.InsertWallet(t, sigChain)
	sweep := fixtures.InsertTransaction(t, wallet.ID, nil, sigChain, models.TxTypeSweep, "confirmed", models.NativeSOL, "7", 1600)
	_, err := facades.Orm().Query().Exec(`UPDATE transactions SET from_address = ? WHERE id = ?`, sigCarol, sweep.ID)
	require.NoError(t, err)

	summary, listed := f.syncListing(t, clock)

	assert.Zero(t, summary.MovedAccounts)
	assert.Equal(t, sigCarol, listed[0], "the sender comes first")
	assert.Equal(t, []string{"sig-carol"}, sigDepositHashes(t, sigCarol))
}

// States that cannot be read flag nothing: the cycle reads its owners in turn, as
// without states, and counts no failure.
func TestSync_Signatures_ReadsInTurnWhenTheAccountStatesCannotBeRead(t *testing.T) {
	f := newSignatureFixture(t)
	states := f.withStates()
	states.statesErr = errors.New("429 too many requests")
	f.chain.history(sigBob, f.chain.tx("sig-bob", 1500, lamports(sigBob, 10, 1)))

	summary := f.sync(t)

	assert.Zero(t, summary.FailedAccounts)
	assert.Zero(t, summary.MovedAccounts)
	assert.Equal(t, []string{"sig-bob"}, sigDepositHashes(t, sigBob), "the rotation reads it")
}

// The states are read a hundred accounts a batch, every known account of every owner
// once (an owner this process never listed by the accounts it derives).
func TestSync_Signatures_ReadsTheStatesOfEveryAccountInBatches(t *testing.T) {
	f := newSignatureFixture(t)
	states := f.withStates()
	var owners []string
	for i := range 120 {
		owners = append(owners, fmt.Sprintf("owner-%03d", i))
	}
	f.watch(t, owners...)

	f.sync(t)

	var read []string
	for _, batch := range states.batches {
		assert.LessOrEqual(t, len(batch), chain.AccountStateBatch)
		read = append(read, batch...)
	}
	assert.Len(t, read, 2+1+120, "alice's two accounts, bob and the 120 owners, once each")
	assert.Len(t, states.batches, 2)
}

// The states stored by one process are what the next one compares with: a process
// that never listed an owner reads first, on its first cycle, the one that moved.
func TestSync_Signatures_ANewProcessReadsFirstWhatMovedSinceTheStoredStates(t *testing.T) {
	f := newSignatureFixture(t)
	f.watch(t, sigCarol, sigDave)
	f.withStates()
	f.chain.history(sigDave, f.chain.tx("sig-dave-1", 1500, lamports(sigDave, 10, 1)))
	f.chain.mu.Lock()
	f.chain.heads[sigDave] = sigHead + signatureIndexMargin
	f.chain.mu.Unlock()
	f.sync(t)
	require.Equal(t, "txs:1", f.cursors(t)[sigDave].AccountState)

	restarted := newSignatureDepositSvc(f.registry, newWebhookSvc(), func(context.Context) SignatureSyncSettings { return f.settings })
	restarted.SetPendingStore(f.pending)
	restarted.SetDepositEvents(f.events)
	f.svc = restarted
	clock := f.oneOwnerACycle()
	f.chain.history(sigDave, f.chain.tx("sig-dave-2", 1600, lamports(sigDave, 20, 1)))

	_, listed := f.syncListing(t, clock)

	assert.Equal(t, sigDave, listed[0], "dave moved since his stored state; the others did not")
	assert.ElementsMatch(t, []string{"sig-dave-1", "sig-dave-2"}, sigDepositHashes(t, sigDave))
}

// With account states, the rotation reads a settled address again only every
// signatureRotationEvery, whatever the interval: the cycles in between read only what
// moved. An address not settled yet is read on every cycle.
func TestSync_Signatures_ReadsASettledAddressAgainOnlyEveryRotationWhenNothingMoved(t *testing.T) {
	f := newSignatureFixture(t)
	f.withStates()
	clock := &stepClock{now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	f.svc.now = clock.Now

	_, first := f.syncListing(t, clock)
	_, second := f.syncListing(t, clock)
	assert.ElementsMatch(t, []string{sigAlice, sigBob}, first)
	assert.ElementsMatch(t, []string{sigAlice, sigBob}, second, "not settled after one read of an empty history: read again")
	for account, cursor := range f.cursors(t) {
		require.True(t, cursor.Covered, "account %s settled", account)
	}

	quiet, listed := f.syncListing(t, clock)
	assert.Empty(t, listed, "nothing moved and every address was read a minute ago")
	assert.Zero(t, quiet.MovedAccounts)

	f.chain.history(sigBob, f.chain.tx("sig-bob", 1500, lamports(sigBob, 10, 1)))
	_, listed = f.syncListing(t, clock)
	assert.Equal(t, []string{sigBob}, listed, "only what moved")

	clock.advance(signatureRotationEvery)
	_, listed = f.syncListing(t, clock)
	assert.Contains(t, listed, sigAlice, "the rotation reads alice again once its pace elapsed")
}
