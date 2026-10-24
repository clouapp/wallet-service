package deposit

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
	"github.com/macrowallets/waas/pkg/solanacredit"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	sigChain      = models.ChainTSOL
	sigHead       = 5000
	sigCheckpoint = 1000

	sigAlice     = "alice-owner"
	sigAliceUSDC = "alice-usdc-account"
	sigBob       = "bob-owner"
	sigStranger  = "stranger-owner"
	sigPayer     = "payer-owner"
	sigUSDCMint  = "usdc-mint"
)

var sigUSDC = types.Token{Symbol: models.SymbolUSDC, Contract: sigUSDCMint, Decimals: 6, ChainID: sigChain}

// signatureChain is the Solana port for the signature sync tests: the accounts,
// histories (oldest first), transactions and slot hashes of a node, and what the
// sync asked for. Reading recorded node answers stays in the adapter tests; here the
// port reports their outcome, listing as the adapter does (newest first, below
// before, above until, down to the floor, at most MaxPages pages, an empty end of
// history answered with the node's first available block, and a listing above until
// ended only when until is right below what it listed, as the adapter's confirmation
// call proves). The endpoint can be made to show less: nothing above hideAbove (a
// backend behind on the newest slots), nothing below hideBelow (a node without the
// old ledger, whose first available block is then hideBelow). A listing below a
// before reads, as the adapter does, from right below its anchor (from the newest
// when it has none) and lists and ends nothing unless the before comes first: an
// anchor it does not show answers an empty page (or Agave's -32020, which the adapter
// reads the same), or, with newestOnUnknown, its own newest signatures, as an index
// that answers what it holds instead of failing may. With unknownErrs it fails an
// until it does not show, as Agave does (-32020), else it answers as another provider
// may, with what it holds. With raceMiss, an empty first page
// above a cursor may stay unended, as when a signature lands between the page and its
// confirmation twice in a row. ScanBlock reports the same transactions by slot, as the
// block scan reads them.
type signatureChain struct {
	*mocks.MockChain
	mu           sync.Mutex
	accounts     map[string][]string
	accountErr   map[string]error
	skippedMints []string
	histories    map[string][]chain.AccountSignature
	pageSize     int
	ledgerStart  uint64
	hideAbove    uint64
	hideBelow    uint64
	unknownErrs  bool
	// newestOnUnknown answers a listing below an anchor it does not show from its own
	// newest signature instead of with an empty page.
	newestOnUnknown bool
	raceMiss        func() bool
	heads           map[string]uint64
	transactions    map[string]chain.TransactionTransfers
	txErr           map[string]error
	hashes          map[uint64]string
	hashErr         map[uint64]error
	queries         map[string][]chain.SignatureQuery
	txReads         map[string]int
	hashReads       map[uint64]int
	duringRead      func()
	onTx            func(ctx context.Context, signature string)
}

func newSignatureChain() *signatureChain {
	adapter := mocks.NewMockChain(sigChain)
	adapter.NativeAssetVal = models.NativeSOL
	adapter.RequiredConfirmationsVal = 1
	adapter.GetLatestBlockFn = func(context.Context) (uint64, error) { return sigHead, nil }
	c := &signatureChain{MockChain: adapter, accounts: map[string][]string{}, accountErr: map[string]error{},
		histories: map[string][]chain.AccountSignature{}, heads: map[string]uint64{}, transactions: map[string]chain.TransactionTransfers{},
		txErr: map[string]error{}, hashes: map[uint64]string{}, hashErr: map[uint64]error{},
		queries: map[string][]chain.SignatureQuery{}, txReads: map[string]int{}, hashReads: map[uint64]int{}}
	adapter.ScanBlockFn = func(_ context.Context, slot uint64) ([]types.DetectedTransfer, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		var transfers []types.DetectedTransfer
		for _, tx := range c.transactions {
			if tx.Slot != slot {
				continue
			}
			for _, transfer := range tx.Transfers {
				transfer.BlockHash = c.hashes[slot]
				transfers = append(transfers, transfer)
			}
		}
		return transfers, nil
	}
	return c
}

// AssembleSolana makes the fake a Solana adapter for the deposit service, which, as
// for the live one, does not read again the block of a deposit it recorded from a
// finalized slot.
func (c *signatureChain) AssembleSolana(*types.UnsignedTx, []byte) (*types.SignedTx, error) {
	return nil, errors.New("the signature sync fake signs nothing")
}

func (c *signatureChain) SignatureAccounts(_ context.Context, owner string) (chain.SignatureAccounts, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.accountErr[owner]; err != nil {
		return chain.SignatureAccounts{}, err
	}
	accounts, ok := c.accounts[owner]
	if !ok {
		accounts = []string{owner}
	}
	return chain.SignatureAccounts{Accounts: accounts, SkippedMints: c.skippedMints}, nil
}

func (c *signatureChain) AccountSignatures(_ context.Context, account string, query chain.SignatureQuery) (chain.SignatureHistory, error) {
	c.mu.Lock()
	c.queries[account] = append(c.queries[account], query)
	history, during, pageSize, ledgerStart := c.histories[account], c.duringRead, c.pageSize, c.ledgerStart
	hideAbove, hideBelow, unknownErrs, raceMiss, newestOnUnknown := c.hideAbove, c.hideBelow, c.unknownErrs, c.raceMiss, c.newestOnUnknown
	head, ok := c.heads[account]
	if !ok {
		head = sigHead
	}
	c.mu.Unlock()
	if hideAbove != 0 || hideBelow != 0 {
		var shown []chain.AccountSignature
		for _, signature := range history {
			if (hideAbove == 0 || signature.Slot <= hideAbove) && signature.Slot >= hideBelow {
				shown = append(shown, signature)
			}
		}
		history = shown
		ledgerStart = max(ledgerStart, hideBelow)
	}
	shows := func(signature string) bool {
		for _, shown := range history {
			if shown.Signature == signature {
				return true
			}
		}
		return false
	}
	if unknownErrs && query.Until != "" && !shows(query.Until) {
		return chain.SignatureHistory{}, errors.New("-32020 transaction not found")
	}
	if during != nil {
		during()
	}
	if pageSize == 0 {
		pageSize = 1000
	}
	listed := chain.SignatureHistory{Head: head}
	newestFirst := make([]chain.AccountSignature, len(history))
	for i, signature := range history {
		newestFirst[len(history)-1-i] = signature
	}
	start := 0
	if query.Before != "" {
		at := 0
		if query.Anchor != "" {
			at = -1
			for i, signature := range newestFirst {
				if signature.Signature == query.Anchor {
					at = i + 1
				}
			}
			if at < 0 && !newestOnUnknown {
				return listed, nil
			}
			at = max(at, 0)
		}
		if at >= len(newestFirst) || newestFirst[at].Signature != query.Before {
			return listed, nil
		}
		start = at + 1
	}
	for _, signature := range newestFirst[start:] {
		if query.Until != "" && signature.Signature == query.Until {
			listed.Ended = raceMiss == nil || query.Before != "" || len(listed.Signatures) > 0 || !raceMiss()
			return listed, nil
		}
		if query.HasFloor && signature.Slot < query.FloorSlot {
			listed.Ended, listed.FloorCrossed = true, true
			return listed, nil
		}
		if len(listed.Signatures) == query.MaxPages*pageSize {
			listed.More = true
			return listed, nil
		}
		listed.Signatures = append(listed.Signatures, signature)
	}
	// The end of what the endpoint shows: above until, the signature below the oldest
	// listed is not until, so the adapter's confirmation proves nothing.
	listed.Ended = query.Until == ""
	if query.Until == "" {
		listed.LedgerStart = ledgerStart
	}
	return listed, nil
}

func (c *signatureChain) TransactionTransfers(ctx context.Context, signature string) (chain.TransactionTransfers, error) {
	if hook := c.onTx; hook != nil {
		hook(ctx, signature)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.txReads[signature]++
	if err := c.txErr[signature]; err != nil {
		return chain.TransactionTransfers{}, err
	}
	tx, ok := c.transactions[signature]
	if !ok {
		return chain.TransactionTransfers{}, fmt.Errorf("transaction %s unknown to the node", signature)
	}
	return chain.TransactionTransfers{Slot: tx.Slot, Transfers: append([]types.DetectedTransfer(nil), tx.Transfers...)}, nil
}

func (c *signatureChain) BlockHashAt(_ context.Context, slot uint64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hashReads[slot]++
	if err := c.hashErr[slot]; err != nil {
		return "", err
	}
	return c.hashes[slot], nil
}

// tx records a finalized transaction at slot whose credits are transfers.
func (c *signatureChain) tx(signature string, slot uint64, transfers ...types.DetectedTransfer) chain.AccountSignature {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range transfers {
		transfers[i].TxHash, transfers[i].BlockNumber = signature, slot
	}
	c.transactions[signature] = chain.TransactionTransfers{Slot: slot, Transfers: transfers}
	c.hashes[slot] = fmt.Sprintf("hash-of-slot-%d", slot)
	return chain.AccountSignature{Signature: signature, Slot: slot}
}

func (c *signatureChain) history(account string, signatures ...chain.AccountSignature) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.histories[account] = append(c.histories[account], signatures...)
}

func (c *signatureChain) queriesOf(account string) []chain.SignatureQuery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]chain.SignatureQuery(nil), c.queries[account]...)
}

func (c *signatureChain) reads(signature string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.txReads[signature]
}

func (c *signatureChain) hashReadsOf(slot uint64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hashReads[slot]
}

func failed(signature string, slot uint64) chain.AccountSignature {
	return chain.AccountSignature{Signature: signature, Slot: slot, Failed: true}
}

func lamports(to string, amount int64, index int) types.DetectedTransfer {
	return types.DetectedTransfer{From: sigPayer, To: to, Amount: big.NewInt(amount), Asset: models.NativeSOL, LogIndex: index}
}

func usdcCredit(owner string, amount int64, tokenAccountIndex int) types.DetectedTransfer {
	token := sigUSDC
	return types.DetectedTransfer{From: sigPayer, To: owner, Amount: big.NewInt(amount), Asset: token.Symbol, Token: &token,
		LogIndex: solanacredit.TokenPosition(tokenAccountIndex)}
}

type signatureFixture struct {
	svc      *Service
	chain    *signatureChain
	registry *chain.Registry
	events   *publishedDeposits
	settings SignatureSyncSettings
	pending  pending.Store
}

// newSignatureFixture watches alice (her owner account and her USDC account) and
// bob on tsol, with the chain's scan checkpoint at sigCheckpoint and tsol in the
// signature sync's settings.
func newSignatureFixture(t *testing.T) *signatureFixture {
	t.Helper()
	fixtures.TestDB(t)
	f := &signatureFixture{chain: newSignatureChain(), events: &publishedDeposits{},
		settings: SignatureSyncSettings{Chains: []string{sigChain}, Interval: time.Minute, Concurrency: 2, MaxPages: 10}}
	f.registry = chain.NewRegistry()
	f.registry.RegisterChain(f.chain)
	f.registry.RegisterToken(sigUSDC)
	f.svc = newSignatureDepositSvc(f.registry, newWebhookSvc(), func(context.Context) SignatureSyncSettings { return f.settings })
	f.svc.SetDepositEvents(f.events)
	store, err := pending.NewFileStore(pending.FileStoreDeps{Dir: t.TempDir()})
	require.NoError(t, err)
	f.pending = store
	f.svc.SetPendingStore(store)
	for _, owner := range []string{sigAlice, sigBob} {
		wallet := fixtures.InsertWallet(t, sigChain)
		fixtures.InsertAddress(t, wallet.ID, sigChain, owner, "user-"+owner, 0)
	}
	// Only alice and bob are watched: the base addresses InsertWallet creates are not.
	_, err = facades.Orm().Query().Exec(`UPDATE addresses SET is_active = false WHERE chain = ? AND address NOT IN (?, ?)`, sigChain, sigAlice, sigBob)
	require.NoError(t, err)
	f.chain.accounts[sigAlice] = []string{sigAlice, sigAliceUSDC}
	require.NoError(t, f.svc.saveCheckpoint(context.Background(), sigChain, sigCheckpoint))
	return f
}

func (f *signatureFixture) sync(t *testing.T) SignatureSyncSummary {
	t.Helper()
	summary, err := f.svc.SyncSignatures(context.Background(), sigChain)
	require.NoError(t, err)
	return summary
}

// syncCovered runs two cycles, so the end of every history the endpoint shows (an
// empty page) is seen twice in a row, which covers the accounts; it returns the second.
func (f *signatureFixture) syncCovered(t *testing.T) SignatureSyncSummary {
	t.Helper()
	f.sync(t)
	summary := f.sync(t)
	for account, cursor := range f.cursors(t) {
		require.True(t, cursor.Covered, "account %s covered after the end of its history showed twice", account)
	}
	return summary
}

// sigCovered is the slot the fixture's accounts are read up to at the head sigHead:
// the head less the index margin.
const sigCovered = sigHead - signatureIndexMargin

func (f *signatureFixture) cursors(t *testing.T) map[string]models.DepositSignatureCursor {
	t.Helper()
	rows, err := f.svc.signatureCursors.FindByChain(context.Background(), sigChain)
	require.NoError(t, err)
	byAccount := map[string]models.DepositSignatureCursor{}
	for _, row := range rows {
		byAccount[row.Account] = row
	}
	return byAccount
}

func (f *signatureFixture) keepPending(t *testing.T, blocks ...uint64) {
	t.Helper()
	now := time.Now().UTC()
	for _, block := range blocks {
		require.NoError(t, f.pending.Put(context.Background(), pending.Entry{Chain: sigChain, Block: block, Attempts: 3,
			ErrorClass: pending.ClassRPC, LastError: "429 max usage reached", FirstFailedAt: now, LastFailedAt: now, NextRetryAt: now.Add(time.Hour)}))
	}
}

func (f *signatureFixture) pendingBlocks(t *testing.T) []uint64 {
	t.Helper()
	entries, err := f.pending.List(context.Background(), sigChain)
	require.NoError(t, err)
	var blocks []uint64
	for _, entry := range entries {
		blocks = append(blocks, entry.Block)
	}
	return blocks
}

func sigDeposits(t *testing.T) []models.Transaction {
	t.Helper()
	var rows []models.Transaction
	require.NoError(t, facades.Orm().Query().Where("chain", sigChain).Where("tx_type", models.TxTypeDeposit).
		Order("block_number").Order("log_index").Find(&rows))
	return rows
}

// sameCursor checks the recorded stretch of an account: its newest recorded signature
// and slot, its oldest one, its floor, and whether it is read down to the floor.
func sameCursor(t *testing.T, got models.DepositSignatureCursor, top string, topSlot int64, bottom string, floor int64, covered bool) {
	t.Helper()
	assert.Equal(t, top, got.Signature, "newest recorded signature")
	assert.Equal(t, topSlot, got.Slot, "its slot")
	assert.Equal(t, bottom, got.BackfillBefore, "oldest recorded signature")
	assert.Equal(t, floor, got.SyncedFromSlot, "floor")
	assert.Equal(t, covered, got.Covered, "read down to the floor")
}

func TestSync_Signatures_RecordsTheDepositsOfEveryAccountOnceAndConfirmsThemInTheSameCycle(t *testing.T) {
	f := newSignatureFixture(t)
	native := f.chain.tx("sig-native", 1100, lamports(sigAlice, 5_000_000, 1), lamports(sigStranger, 7, 2))
	burned := failed("sig-failed", 1101)
	// One transaction moves alice's USDC: her owner account and her token account both
	// list it, so it is read and recorded once.
	token := f.chain.tx("sig-usdc", 1102, usdcCredit(sigAlice, 2_200, 3))
	nobody := f.chain.tx("sig-nobody", 1103, lamports(sigStranger, 9, 1))
	toBob := f.chain.tx("sig-bob", 1104, lamports(sigBob, 42, 4))
	f.chain.history(sigAlice, native, burned, token, nobody)
	f.chain.history(sigAliceUSDC, token)
	f.chain.history(sigBob, toBob)
	probe := &lockProbe{svc: f.svc, chainID: sigChain}
	f.svc.SetBalanceRefresher(probe)

	summary := f.sync(t)

	assert.Equal(t, SignatureSyncSummary{Accounts: 3, NewSignatures: 6, Deposits: 3, BackfillingAccounts: 3, ListedHead: sigHead, Head: sigHead}, summary,
		"every history ended with an empty page, seen once: no account is covered yet")
	rows := sigDeposits(t)
	require.Len(t, rows, 3)
	want := []struct {
		hash, to, amount, asset string
		slot                    int64
		position                int
	}{
		{"sig-native", sigAlice, "5000000", models.NativeSOL, 1100, 1},
		{"sig-usdc", sigAlice, "2200", models.SymbolUSDC, 1102, 256 + 3},
		{"sig-bob", sigBob, "42", models.NativeSOL, 1104, 4},
	}
	for i, w := range want {
		row := rows[i]
		assert.Equal(t, w.hash, row.TxHash)
		assert.Equal(t, w.to, row.ToAddress)
		assert.Equal(t, w.amount, row.Amount)
		assert.Equal(t, w.asset, row.Asset)
		assert.Equal(t, w.slot, row.BlockNumber)
		assert.Equal(t, w.position, row.LogIndex)
		assert.Equal(t, fmt.Sprintf("hash-of-slot-%d", w.slot), row.BlockHash, "the hash of the slot that holds the transaction")
		assert.Equal(t, models.TxSourceChain, row.Source)
		assert.Equal(t, string(types.TxStatusConfirmed), row.Status, "a finalized slot confirms in the cycle that records it")
		assert.Equal(t, int(sigHead-w.slot+1), row.Confirmations, "counted at the head")
	}
	assert.Equal(t, sigUSDCMint, rows[1].TokenContract)
	assert.Len(t, f.events.pending(), 3)
	assert.Len(t, f.events.ids[types.EventDepositConfirmed], 3)
	assert.Equal(t, 1, f.chain.reads("sig-usdc"), "a transaction two accounts list is read once per cycle")
	assert.Zero(t, f.chain.reads("sig-failed"), "a failed transaction is never read")
	assert.Zero(t, f.chain.hashReadsOf(1103), "a slot that credits no watched address needs no hash")
	assert.Equal(t, 1, f.chain.hashReadsOf(1102))
	assert.Len(t, probe.freeSeen, 2, "the wallets of alice and bob are refreshed")
	for _, free := range probe.freeSeen {
		assert.True(t, free, "balances are refreshed once the chain's scan lock is released")
	}

	cursors := f.cursors(t)
	require.Len(t, cursors, 3)
	sameCursor(t, cursors[sigAlice], "sig-nobody", 1103, "sig-native", sigCheckpoint, false)
	sameCursor(t, cursors[sigAliceUSDC], "sig-usdc", 1102, "sig-usdc", sigCheckpoint, false)
	sameCursor(t, cursors[sigBob], "sig-bob", 1104, "sig-bob", sigCheckpoint, false)
	assert.True(t, cursors[sigBob].EndSeen, "the end of bob's history was seen once")

	second := f.sync(t)

	assert.Equal(t, SignatureSyncSummary{Accounts: 3, CoveredHead: sigCovered, ListedHead: sigHead, Head: sigHead}, second,
		"the end seen a second time covers every account, read up to the head less the index margin")
	cursors = f.cursors(t)
	sameCursor(t, cursors[sigAlice], "sig-nobody", 1103, "sig-native", sigCheckpoint, true)
	sameCursor(t, cursors[sigAliceUSDC], "sig-usdc", 1102, "sig-usdc", sigCheckpoint, true)
	sameCursor(t, cursors[sigBob], "sig-bob", 1104, "sig-bob", sigCheckpoint, true)

	again := f.sync(t)

	assert.Equal(t, SignatureSyncSummary{Accounts: 3, CoveredHead: sigCovered, ListedHead: sigHead, Head: sigHead}, again, "nothing newer than the cursors")
	assert.Len(t, sigDeposits(t), 3)
	assert.Len(t, f.events.pending(), 3)
	queries := f.chain.queriesOf(sigAlice)
	require.Len(t, queries, 4)
	assert.Equal(t, chain.SignatureQuery{FloorSlot: sigCheckpoint, HasFloor: true, MaxPages: signatureBackfillPages}, queries[0],
		"a new account is read from the head down to its floor")
	assert.Equal(t, chain.SignatureQuery{Until: "sig-nobody", FloorSlot: 1103, HasFloor: true, MaxPages: signatureForwardPageCeiling}, queries[1],
		"then above its cursor")
	assert.Equal(t, chain.SignatureQuery{Before: "sig-native", Anchor: "sig-failed", FloorSlot: sigCheckpoint, HasFloor: true, MaxPages: signatureBackfillPages}, queries[2],
		"and below it again, where the end shows a second time")
	assert.Equal(t, chain.SignatureQuery{Until: "sig-nobody", FloorSlot: 1103, HasFloor: true, MaxPages: signatureForwardPageCeiling}, queries[3],
		"a covered account is read above its cursor only")
}

func TestSync_Signatures_AFailingSignatureStopsItsAccountsStretchBeforeIt(t *testing.T) {
	failures := map[string]func(f *signatureFixture, signature string, slot uint64){
		"the transaction is not read": func(f *signatureFixture, signature string, _ uint64) {
			f.chain.txErr[signature] = errors.New("429 max usage reached")
		},
		"its slot's hash is not read": func(f *signatureFixture, _ string, slot uint64) {
			f.chain.hashErr[slot] = errors.New("slot not available")
		},
	}
	for name, fail := range failures {
		t.Run("backfill, "+name, func(t *testing.T) {
			f := newSignatureFixture(t)
			f.chain.history(sigBob,
				f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)),
				f.chain.tx("sig-2", 1202, lamports(sigBob, 20, 1)),
				f.chain.tx("sig-3", 1203, lamports(sigBob, 30, 1)))
			fail(f, "sig-2", 1202)
			activity := newMemoryActivity()
			f.svc.SetScanActivityStore(activity)

			summary := f.sync(t)

			assert.Equal(t, 1, summary.FailedAccounts)
			assert.Equal(t, 1, summary.Deposits, "the history is read newest first: sig-3, then sig-2 fails")
			assert.Zero(t, summary.CoveredHead, "an account failed: no slot is covered")
			sameCursor(t, f.cursors(t)[sigBob], "sig-3", 1203, "sig-3", sigCheckpoint, false)
			record, _ := activity.syncOf(sigChain)
			assert.Equal(t, 1, record.FailedAccounts, "the health check sees the failed account")

			f.chain.mu.Lock()
			f.chain.txErr, f.chain.hashErr = map[string]error{}, map[uint64]error{}
			f.chain.mu.Unlock()
			summary = f.sync(t)

			assert.Zero(t, summary.FailedAccounts)
			assert.Equal(t, 2, summary.Deposits)
			assert.Len(t, sigDeposits(t), 3)
			sameCursor(t, f.cursors(t)[sigBob], "sig-3", 1203, "sig-1", sigCheckpoint, false)
			queries := f.chain.queriesOf(sigBob)
			assert.Equal(t, "sig-3", queries[len(queries)-1].Before, "the retry reads the history below the stretch recorded")

			f.sync(t)
			sameCursor(t, f.cursors(t)[sigBob], "sig-3", 1203, "sig-1", sigCheckpoint, true)
		})
		t.Run("new signatures, "+name, func(t *testing.T) {
			f := newSignatureFixture(t)
			f.chain.history(sigBob, f.chain.tx("sig-0", 1100, lamports(sigBob, 1, 1)))
			f.syncCovered(t)
			f.chain.history(sigBob,
				f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)),
				f.chain.tx("sig-2", 1202, lamports(sigBob, 20, 1)),
				f.chain.tx("sig-3", 1203, lamports(sigBob, 30, 1)))
			fail(f, "sig-2", 1202)

			summary := f.sync(t)

			assert.Equal(t, 1, summary.FailedAccounts)
			assert.Equal(t, 1, summary.Deposits, "new signatures are read oldest first: sig-1, then sig-2 fails")
			assert.Zero(t, summary.CoveredHead, "an account failed: no slot is covered")
			sameCursor(t, f.cursors(t)[sigBob], "sig-1", 1201, "sig-0", sigCheckpoint, true)

			f.chain.mu.Lock()
			f.chain.txErr, f.chain.hashErr = map[string]error{}, map[uint64]error{}
			f.chain.mu.Unlock()
			summary = f.sync(t)

			assert.Equal(t, 2, summary.Deposits)
			assert.Len(t, sigDeposits(t), 4)
			sameCursor(t, f.cursors(t)[sigBob], "sig-3", 1203, "sig-0", sigCheckpoint, true)
			queries := f.chain.queriesOf(sigBob)
			assert.Equal(t, "sig-1", queries[len(queries)-1].Until, "the retry reads above the last recorded signature")
		})
	}
}

func TestSync_Signatures_AFailureAtTheFirstSignatureWritesNoCursor(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
	f.chain.txErr["sig-1"] = errors.New("node behind")

	summary := f.sync(t)

	assert.Equal(t, 1, summary.FailedAccounts)
	_, hasCursor := f.cursors(t)[sigBob]
	assert.False(t, hasCursor, "an account read from the floor again on the next cycle")
	assert.Empty(t, sigDeposits(t))
}

func TestSync_Signatures_ReadsANewAccountBackToTheCheckpointLoweredToTheLowestPendingBlock(t *testing.T) {
	f := newSignatureFixture(t)
	f.keepPending(t, 950, 980)
	before := f.chain.tx("sig-before", 949, lamports(sigBob, 1, 1))
	pendingOnly := f.chain.tx("sig-pending", 950, lamports(sigBob, 2, 1))
	since := f.chain.tx("sig-since", 1500, lamports(sigBob, 3, 1))
	f.chain.history(sigBob, before, pendingOnly, since)

	first := f.sync(t)

	assert.Equal(t, chain.SignatureQuery{FloorSlot: 950, HasFloor: true, MaxPages: signatureBackfillPages}, f.chain.queriesOf(sigBob)[0],
		"the floor is the lowest pending block, below the checkpoint")
	rows := sigDeposits(t)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"sig-pending", "sig-since"}, []string{rows[0].TxHash, rows[1].TxHash},
		"the deposit of a pending block is recovered, nothing below the floor is read")
	assert.True(t, f.cursors(t)[sigBob].Covered, "a signature below the floor proves bob's history read down to it at once")
	assert.False(t, f.cursors(t)[sigAlice].Covered, "an empty page proves alice's only once it shows again")
	assert.Zero(t, first.ResolvedPending)

	summary := f.sync(t)

	assert.Equal(t, 2, summary.ResolvedPending)
	assert.Empty(t, f.pendingBlocks(t), "every account read from 950 on covers the pending blocks")
	for account, cursor := range f.cursors(t) {
		assert.Equal(t, int64(950), cursor.SyncedFromSlot, "account %s", account)
		assert.True(t, cursor.Covered, "account %s", account)
	}
}

func TestSync_Signatures_KeepsThePendingBlocksAnAccountsHistoryDoesNotCover(t *testing.T) {
	f := newSignatureFixture(t)
	// bob's history was first read down to slot 990 (an earlier cycle), after block 980
	// went pending: his deposits in it, if any, are not covered.
	f.chain.history(sigBob, failed("sig-old", 995))
	require.NoError(t, f.svc.signatureCursors.Advance(context.Background(), models.DepositSignatureCursor{
		Chain: sigChain, Account: sigBob, Signature: "sig-old", Slot: 995, BackfillBefore: "sig-old", SyncedFromSlot: 990, Covered: true}))
	f.keepPending(t, 980, 995)

	summary := f.syncCovered(t)

	assert.Equal(t, 1, summary.ResolvedPending)
	assert.Equal(t, []uint64{980}, f.pendingBlocks(t), "block 980 stays pending for the pending retry")
	assert.Equal(t, chain.SignatureQuery{Until: "sig-old", FloorSlot: 995, HasFloor: true, MaxPages: signatureForwardPageCeiling}, f.chain.queriesOf(sigBob)[0])
}

func TestSync_Signatures_KeepsEveryPendingBlockWhileAnAccountFails(t *testing.T) {
	for name, fail := range map[string]func(f *signatureFixture){
		"an account's history":       func(f *signatureFixture) { f.chain.txErr["sig-1"] = errors.New("429") },
		"an address's accounts list": func(f *signatureFixture) { f.chain.accountErr[sigAlice] = errors.New("429") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			f.syncCovered(t)
			f.keepPending(t, 1050)
			// A new signature, above the slot bob is known empty up to.
			f.chain.history(sigBob, f.chain.tx("sig-1", sigCovered+50, lamports(sigBob, 10, 1)))
			fail(f)

			summary := f.sync(t)

			assert.Equal(t, 1, summary.FailedAccounts)
			assert.Zero(t, summary.ResolvedPending)
			assert.Zero(t, summary.CoveredHead, "the other accounts were read up to the head, this one was not")
			assert.Equal(t, []uint64{1050}, f.pendingBlocks(t), "a pending block is dropped only once every account was read")
		})
	}
}

func TestSync_Signatures_WithoutACheckpointReadsAtMostMaxPagesOfEachHistory(t *testing.T) {
	f := newSignatureFixture(t)
	require.NoError(t, f.svc.checkpoints.Delete(context.Background(), sigChain))
	f.settings.MaxPages = 2
	f.chain.pageSize = 1
	for slot := uint64(10); slot < 15; slot++ {
		f.chain.history(sigBob, f.chain.tx(fmt.Sprintf("sig-%d", slot), slot, lamports(sigBob, int64(slot), 1)))
	}

	summary := f.sync(t)

	assert.Equal(t, chain.SignatureQuery{MaxPages: 2}, f.chain.queriesOf(sigBob)[0], "no floor: the page limit bounds the history")
	assert.Equal(t, 2, summary.Deposits, "the two newest pages")
	sameCursor(t, f.cursors(t)[sigBob], "sig-14", 14, "sig-13", 13, true)
}

func TestSync_Signatures_RecordsOneRowForADepositTheBlockScanOrThePushAlsoSaw(t *testing.T) {
	setup := func(t *testing.T) *signatureFixture {
		f := newSignatureFixture(t)
		f.chain.history(sigAlice, f.chain.tx("sig-both", 1300, lamports(sigAlice, 77, 1), usdcCredit(sigAlice, 5_000, 4)))
		return f
	}
	push := func(t *testing.T, f *signatureFixture) {
		t.Helper()
		token := sigUSDC
		// Helius sends no block hash; it finds the scanner's credits at the scanner's positions.
		transfers := []providers.InboundTransfer{
			{TxHash: "sig-both", BlockNumber: 1300, From: sigPayer, To: sigAlice, Amount: big.NewInt(77), LogIndex: 1},
			{TxHash: "sig-both", BlockNumber: 1300, From: sigPayer, To: sigAlice, Amount: big.NewInt(5_000), Asset: "USDC", Token: &token, LogIndex: 256 + 4},
		}
		ingestSvc := newIngestSvc(f.registry)
		ingestSvc.SetDepositEvents(f.events)
		require.NoError(t, ingestSvc.Ingest(context.Background(), ingest.InboundEvent{ChainID: sigChain, Transfers: transfers}))
	}
	scan := func(t *testing.T, f *signatureFixture) {
		t.Helper()
		_, err := f.svc.ScanBlock(context.Background(), sigChain, 1300)
		require.NoError(t, err)
	}
	for name, tc := range map[string]struct {
		before, after func(t *testing.T, f *signatureFixture)
		source        string
	}{
		"block scan first":                       {before: scan, source: models.TxSourceChain},
		"push first":                             {before: push, source: models.TxSourceDepositIngest},
		"sync first, then the scan and the push": {after: func(t *testing.T, f *signatureFixture) { scan(t, f); push(t, f) }, source: models.TxSourceChain},
	} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			if tc.before != nil {
				tc.before(t, f)
				require.Len(t, sigDeposits(t), 2)
			}

			summary := f.sync(t)
			if tc.after != nil {
				tc.after(t, f)
			}

			if tc.before != nil {
				assert.Zero(t, summary.Deposits, "the deposits are recorded already")
			}
			rows := sigDeposits(t)
			require.Len(t, rows, 2, "one row per transfer, whichever source saw it first")
			for _, row := range rows {
				assert.Equal(t, tc.source, row.Source)
				assert.Equal(t, "hash-of-slot-1300", row.BlockHash, "a row without a hash gets the hash of the source that has it")
			}
			assert.Equal(t, []int{1, 256 + 4}, []int{rows[0].LogIndex, rows[1].LogIndex})
			assert.Len(t, f.events.pending(), 2, "deposit.pending once per deposit")
		})
	}
}

// The sweep executor records a sweep and its gas seed under the wallet before they
// land; the transactions then credit a watched address, and are no deposits.
func TestSync_Signatures_LeavesOutTheSweepsAndGasSeedsOfTheWallet(t *testing.T) {
	f := newSignatureFixture(t)
	var bob models.Address
	require.NoError(t, facades.Orm().Query().Where("chain", sigChain).Where("address", sigBob).First(&bob))
	for slot, txType := range map[uint64]string{1400: models.TxTypeSweep, 1401: models.TxTypeGasSeed} {
		signature := "sig-" + txType
		require.NoError(t, facades.Orm().Query().Create(&models.Transaction{
			ID: uuid.New(), WalletID: bob.WalletID, ExternalUserID: "user-" + sigBob, Chain: sigChain, TxType: txType,
			TxHash: signature, ToAddress: sigBob, Amount: "1000", Asset: models.NativeSOL, RequiredConfs: 1,
			Status: string(types.TxStatusConfirming), Direction: models.TxDirectionSelf, Source: models.TxSourceWithdrawalFlow,
			RawPayload: "{}", LogIndex: models.TransactionLogIndexNone,
		}))
		f.chain.tx(signature, slot, lamports(sigBob, 1000, 1))
	}
	f.chain.history(sigBob, chain.AccountSignature{Signature: "sig-sweep", Slot: 1400}, chain.AccountSignature{Signature: "sig-gas_seed", Slot: 1401})

	summary := f.sync(t)

	assert.Zero(t, summary.Deposits)
	assert.Empty(t, sigDeposits(t), "an internal transfer is never a deposit")
	sameCursor(t, f.cursors(t)[sigBob], "sig-gas_seed", 1401, "sig-sweep", sigCheckpoint, false)
}

func TestSync_Signatures_SkipsTheCycleWhileAnotherScanHoldsTheChain(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
	activity := newMemoryActivity()
	f.svc.SetScanActivityStore(activity)
	held, err := cacheguard.Acquire(f.svc.cache, scanLockKey(sigChain), time.Minute)
	require.NoError(t, err)
	require.True(t, held)

	summary, err := f.svc.SyncSignatures(context.Background(), sigChain)

	require.NoError(t, err)
	assert.Equal(t, SignatureSyncSummary{Skipped: true}, summary)
	assert.Empty(t, f.chain.queriesOf(sigBob), "nothing is read")
	assert.Empty(t, sigDeposits(t))
	assert.Empty(t, activity.signatures, "a skipped cycle records nothing, so the sync time ages")
}

func TestSync_Signatures_HoldsTheChainsScanLockUnderItsOwnTokenForTheWholeCycle(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
	var mu sync.Mutex
	var blocked []error
	var tokens []string
	f.chain.duringRead = func() {
		_, err := f.svc.lockChain(sigChain)
		mu.Lock()
		defer mu.Unlock()
		blocked = append(blocked, err)
		tokens = append(tokens, f.svc.cache.GetString(scanLockKey(sigChain)))
	}

	f.sync(t)

	require.NotEmpty(t, blocked)
	for i, err := range blocked {
		assert.ErrorIs(t, err, ErrScanInProgress, "a block scan of the chain cannot start during the sync")
		assert.NotEqual(t, "1", tokens[i], "the sync holds the lock under an owner token, not the scan's plain value")
		assert.NotEmpty(t, tokens[i])
	}
	release, err := f.svc.lockChain(sigChain)
	require.NoError(t, err, "the lock is released once the cycle ends")
	release()
}

// A cycle that lost the chain's scan lock (another process took it once it lapsed)
// stops: it keeps what it recorded, never drops a pending block or confirms anything
// under a lock it no longer holds, and never frees the other holder's lock.
func TestSync_Signatures_StopsWhenItLosesTheChainsScanLock(t *testing.T) {
	f := newSignatureFixture(t)
	f.keepPending(t, 1100)
	f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
	f.chain.onTx = func(context.Context, string) {
		require.NoError(t, f.svc.cache.Put(scanLockKey(sigChain), "another-holder", time.Minute))
	}
	activity := newMemoryActivity()
	f.svc.SetScanActivityStore(activity)

	_, err := f.svc.SyncSignatures(context.Background(), sigChain)

	require.ErrorIs(t, err, ErrScanLockLost)
	assert.Equal(t, "another-holder", f.svc.cache.GetString(scanLockKey(sigChain)), "the other holder's lock is left")
	rows := sigDeposits(t)
	require.Len(t, rows, 1)
	assert.Equal(t, string(types.TxStatusPending), rows[0].Status, "nothing is confirmed without the lock")
	assert.Equal(t, []uint64{1100}, f.pendingBlocks(t), "nothing is dropped without the lock")
	assert.Equal(t, "sig-1", f.cursors(t)[sigBob].Signature, "what was recorded is kept")
	assert.Empty(t, activity.signatures, "a cycle that lost its lock records no completed cycle")
}

// The cycle checks the chain's scan lock before each signature it records, so once
// another process took it the cycle stops at once, keeping what it recorded.
func TestSync_Signatures_StopsRecordingAsSoonAsItFindsTheLockLost(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.history(sigBob,
		f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)),
		f.chain.tx("sig-2", 1202, lamports(sigBob, 20, 1)),
		f.chain.tx("sig-3", 1203, lamports(sigBob, 30, 1)))
	f.chain.onTx = func(_ context.Context, signature string) {
		if signature == "sig-3" { // the history is read newest first
			assert.NoError(t, f.svc.cache.Put(scanLockKey(sigChain), "another-holder", time.Minute))
		}
	}

	_, err := f.svc.SyncSignatures(context.Background(), sigChain)

	require.ErrorIs(t, err, ErrScanLockLost)
	rows := sigDeposits(t)
	require.Len(t, rows, 1, "sig-3, recorded as the lock was taken, and nothing after it")
	assert.Equal(t, "sig-3", rows[0].TxHash)
	sameCursor(t, f.cursors(t)[sigBob], "sig-3", 1203, "sig-3", sigCheckpoint, false)
}

// A cycle that finds the lock lost before an account starts reads nothing of it.
func TestSync_Signatures_ReadsNoAccountOnceItFindsTheLockLost(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
	var once sync.Once
	f.chain.GetLatestBlockFn = func(context.Context) (uint64, error) {
		// The head read before the addresses are listed: another process takes the lock.
		once.Do(func() {
			assert.NoError(t, f.svc.cache.Put(scanLockKey(sigChain), "another-holder", time.Minute))
		})
		return sigHead, nil
	}

	_, err := f.svc.SyncSignatures(context.Background(), sigChain)

	require.ErrorIs(t, err, ErrScanLockLost)
	for _, account := range []string{sigAlice, sigAliceUSDC, sigBob} {
		assert.Empty(t, f.chain.queriesOf(account), "no history of %s is read", account)
	}
	assert.Empty(t, f.cursors(t))
}

// A lock found lost stops the whole cycle: the reads the other accounts have in
// flight are cut, not left to finish.
func TestSync_Signatures_ALostLockCutsTheReadsOtherAccountsHaveInFlight(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.accounts[sigAlice] = []string{sigAlice}
	f.chain.history(sigAlice, f.chain.tx("sig-a1", 1201, lamports(sigAlice, 10, 1)), f.chain.tx("sig-a2", 1202, lamports(sigAlice, 20, 1)))
	f.chain.history(sigBob, f.chain.tx("sig-b1", 1301, lamports(sigBob, 30, 1)))
	bobReading := make(chan struct{})
	bobCut := make(chan bool, 1)
	f.chain.onTx = func(ctx context.Context, signature string) {
		switch signature {
		case "sig-b1":
			close(bobReading)
			select {
			case <-ctx.Done():
				bobCut <- true
			case <-time.After(5 * time.Second):
				bobCut <- false
			}
		case "sig-a2": // alice's newest, read first: the lock is taken while bob reads
			select {
			case <-bobReading:
			case <-time.After(5 * time.Second):
			}
			assert.NoError(t, f.svc.cache.Put(scanLockKey(sigChain), "another-holder", time.Minute))
		}
	}

	_, err := f.svc.SyncSignatures(context.Background(), sigChain)

	require.ErrorIs(t, err, ErrScanLockLost)
	select {
	case cut := <-bobCut:
		assert.True(t, cut, "bob's read in flight is cut once alice's account finds the lock lost")
	default:
		t.Fatal("bob's transaction was never read")
	}
}

func TestSync_Signatures_FailsTheCycleWhenItsFloorCannotBeRead(t *testing.T) {
	cases := map[string]func(f *signatureFixture){
		"the checkpoint": func(f *signatureFixture) {
			f.svc.checkpoints = failingCheckpoints{}
		},
		"the pending blocks": func(f *signatureFixture) {
			f.svc.SetPendingStore(failingPending{})
		},
		"a backend of the pending blocks": func(f *signatureFixture) {
			file, ok := f.pending.(*pending.FileStore)
			require.True(t, ok)
			degraded, err := pending.NewDurableStore(pending.DurableStoreDeps{Redis: failingPending{}, File: file})
			require.NoError(t, err)
			f.svc.SetPendingStore(degraded)
		},
	}
	for name, fail := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			f.keepPending(t, 950)
			f.chain.history(sigBob, f.chain.tx("sig-1", 960, lamports(sigBob, 10, 1)))
			fail(f)

			_, err := f.svc.SyncSignatures(context.Background(), sigChain)

			require.Error(t, err, "a floor set too high would skip deposits for good")
			assert.Empty(t, f.chain.queriesOf(sigBob), "no account is read")
			assert.Empty(t, f.cursors(t))
		})
	}
}

// failingCheckpoints is a checkpoint store whose database is down.
type failingCheckpoints struct{}

var errCheckpointsDown = errors.New("dial tcp 10.0.0.2:5432: connection refused")

func (failingCheckpoints) Find(context.Context, string) (uint64, error) { return 0, errCheckpointsDown }
func (failingCheckpoints) Advance(context.Context, string, uint64) (uint64, error) {
	return 0, errCheckpointsDown
}
func (failingCheckpoints) Set(context.Context, string, uint64) error { return errCheckpointsDown }
func (failingCheckpoints) Delete(context.Context, string) error      { return errCheckpointsDown }

// failingPending is a pending store whose backend is down.
type failingPending struct{}

var errPendingDown = errors.New("redis down")

func (failingPending) Put(context.Context, pending.Entry) error     { return errPendingDown }
func (failingPending) Delete(context.Context, string, uint64) error { return errPendingDown }
func (failingPending) List(context.Context, string) ([]pending.Entry, error) {
	return nil, errPendingDown
}

func TestSync_Signatures_RefusesAChainItCannotSync(t *testing.T) {
	f := newSignatureFixture(t)
	f.registry.RegisterChain(mocks.NewMockChain(models.ChainETH))

	_, err := f.svc.SyncSignatures(context.Background(), models.ChainETH)
	require.ErrorIs(t, err, ErrSignaturesUnsupported)
	_, err = f.svc.SyncSignatures(context.Background(), "nochain")
	require.Error(t, err)

	withoutCursors := newDepositSvc(f.registry, nil)
	_, err = withoutCursors.SyncSignatures(context.Background(), sigChain)
	require.ErrorIs(t, err, errNoSignatureCursorStore)
}
