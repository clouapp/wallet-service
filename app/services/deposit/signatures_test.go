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
	sigHead       = 2000
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
// port reports their outcome. ScanBlock reports the same transactions by slot, as
// the block scan reads them.
type signatureChain struct {
	*mocks.MockChain
	mu           sync.Mutex
	accounts     map[string][]string
	accountErr   map[string]error
	histories    map[string][]chain.AccountSignature
	pageSize     int
	transactions map[string]chain.TransactionTransfers
	txErr        map[string]error
	hashes       map[uint64]string
	hashErr      map[uint64]error
	queries      map[string][]chain.SignatureQuery
	txReads      map[string]int
	hashReads    map[uint64]int
	duringRead   func()
}

func newSignatureChain() *signatureChain {
	adapter := mocks.NewMockChain(sigChain)
	adapter.NativeAssetVal = models.NativeSOL
	adapter.RequiredConfirmationsVal = 1
	adapter.GetLatestBlockFn = func(context.Context) (uint64, error) { return sigHead, nil }
	c := &signatureChain{MockChain: adapter, accounts: map[string][]string{}, accountErr: map[string]error{},
		histories: map[string][]chain.AccountSignature{}, transactions: map[string]chain.TransactionTransfers{},
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

func (c *signatureChain) SignatureAccounts(_ context.Context, owner string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.accountErr[owner]; err != nil {
		return nil, err
	}
	if accounts, ok := c.accounts[owner]; ok {
		return accounts, nil
	}
	return []string{owner}, nil
}

// AccountSignatures selects as a node does: the signatures after query.Until, at or
// above the floor, at most MaxPages pages of pageSize (the newest ones, Truncated).
func (c *signatureChain) AccountSignatures(_ context.Context, account string, query chain.SignatureQuery) (chain.SignatureHistory, error) {
	c.mu.Lock()
	c.queries[account] = append(c.queries[account], query)
	history, during, pageSize := c.histories[account], c.duringRead, c.pageSize
	c.mu.Unlock()
	if during != nil {
		during()
	}
	start := 0
	for i, signature := range history {
		if query.Until != "" && signature.Signature == query.Until {
			start = i + 1
		}
	}
	var selected []chain.AccountSignature
	for _, signature := range history[start:] {
		if query.HasFloor && signature.Slot < query.FloorSlot {
			continue
		}
		selected = append(selected, signature)
	}
	if pageSize == 0 {
		pageSize = 1000
	}
	if limit := query.MaxPages * pageSize; len(selected) > limit {
		return chain.SignatureHistory{Signatures: selected[len(selected)-limit:], Truncated: true}, nil
	}
	return chain.SignatureHistory{Signatures: selected}, nil
}

func (c *signatureChain) TransactionTransfers(_ context.Context, signature string) (chain.TransactionTransfers, error) {
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
			ErrorClass: pending.ClassRPC, LastError: "429 max usage reached", FirstFailedAt: now, LastFailedAt: now, NextRetryAt: now}))
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

func cursorAt(signature string, slot int64) models.DepositSignatureCursor {
	return models.DepositSignatureCursor{Signature: signature, Slot: slot}
}

func sameCursor(t *testing.T, want models.DepositSignatureCursor, got models.DepositSignatureCursor, syncedFrom int64) {
	t.Helper()
	assert.Equal(t, want.Signature, got.Signature)
	assert.Equal(t, want.Slot, got.Slot)
	assert.Equal(t, syncedFrom, got.SyncedFromSlot)
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

	assert.Equal(t, SignatureSyncSummary{Accounts: 3, NewSignatures: 6, Deposits: 3}, summary)
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
	sameCursor(t, cursorAt("sig-nobody", 1103), cursors[sigAlice], sigCheckpoint)
	sameCursor(t, cursorAt("sig-usdc", 1102), cursors[sigAliceUSDC], sigCheckpoint)
	sameCursor(t, cursorAt("sig-bob", 1104), cursors[sigBob], sigCheckpoint)

	again := f.sync(t)

	assert.Equal(t, SignatureSyncSummary{Accounts: 3}, again, "nothing newer than the cursors")
	assert.Len(t, sigDeposits(t), 3)
	assert.Len(t, f.events.pending(), 3)
	queries := f.chain.queriesOf(sigAlice)
	require.Len(t, queries, 2)
	assert.Equal(t, chain.SignatureQuery{FloorSlot: sigCheckpoint, HasFloor: true, MaxPages: signatureHistoryPageCeiling}, queries[0])
	assert.Equal(t, chain.SignatureQuery{Until: "sig-nobody", FloorSlot: 1103, HasFloor: true, MaxPages: signatureHistoryPageCeiling}, queries[1],
		"the next cycle reads only what is newer than the cursor")
}

func TestSync_Signatures_AFailingSignatureStopsItsAccountsCursorBeforeIt(t *testing.T) {
	for name, fail := range map[string]func(f *signatureFixture){
		"the transaction is not read": func(f *signatureFixture) { f.chain.txErr["sig-2"] = errors.New("429 max usage reached") },
		"its slot's hash is not read": func(f *signatureFixture) { f.chain.hashErr[1202] = errors.New("slot 1202 not available") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			first := f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1))
			second := f.chain.tx("sig-2", 1202, lamports(sigBob, 20, 1))
			third := f.chain.tx("sig-3", 1203, lamports(sigBob, 30, 1))
			f.chain.history(sigBob, first, second, third)
			fail(f)
			activity := newMemoryActivity()
			f.svc.SetScanActivityStore(activity)

			summary := f.sync(t)

			assert.Equal(t, 1, summary.FailedAccounts)
			assert.Equal(t, 1, summary.Deposits)
			rows := sigDeposits(t)
			require.Len(t, rows, 1, "the signatures after the failing one wait for it")
			assert.Equal(t, "sig-1", rows[0].TxHash)
			sameCursor(t, cursorAt("sig-1", 1201), f.cursors(t)[sigBob], sigCheckpoint)
			record := activity.signatures[sigChain]
			assert.Equal(t, 1, record.FailedAccounts, "the health check sees the failed account")

			f.chain.txErr, f.chain.hashErr = map[string]error{}, map[uint64]error{}
			summary = f.sync(t)

			assert.Zero(t, summary.FailedAccounts)
			assert.Equal(t, 2, summary.Deposits)
			assert.Len(t, sigDeposits(t), 3)
			sameCursor(t, cursorAt("sig-3", 1203), f.cursors(t)[sigBob], sigCheckpoint)
			queries := f.chain.queriesOf(sigBob)
			assert.Equal(t, "sig-1", queries[len(queries)-1].Until, "the retry starts after the last recorded signature")
		})
	}
}

func TestSync_Signatures_AFailureAtTheFirstNewSignatureWritesNoCursor(t *testing.T) {
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

	summary := f.sync(t)

	assert.Equal(t, chain.SignatureQuery{FloorSlot: 950, HasFloor: true, MaxPages: signatureHistoryPageCeiling}, f.chain.queriesOf(sigBob)[0],
		"the floor is the lowest pending block, below the checkpoint")
	rows := sigDeposits(t)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"sig-pending", "sig-since"}, []string{rows[0].TxHash, rows[1].TxHash},
		"the deposit of a pending block is recovered, nothing below the floor is read")
	assert.Equal(t, 2, summary.ResolvedPending)
	assert.Empty(t, f.pendingBlocks(t), "every account read from 950 on covers the pending blocks")
	for account, cursor := range f.cursors(t) {
		assert.Equal(t, int64(950), cursor.SyncedFromSlot, "account %s", account)
	}
}

func TestSync_Signatures_KeepsThePendingBlocksAnAccountsHistoryDoesNotCover(t *testing.T) {
	f := newSignatureFixture(t)
	// bob's history was first read from slot 990 (an earlier cycle), after block 980
	// went pending: his deposits in it, if any, are not covered.
	require.NoError(t, f.svc.signatureCursors.Advance(context.Background(),
		models.DepositSignatureCursor{Chain: sigChain, Account: sigBob, Signature: "sig-old", Slot: 995, SyncedFromSlot: 990}))
	f.keepPending(t, 980, 995)

	summary := f.sync(t)

	assert.Equal(t, 1, summary.ResolvedPending)
	assert.Equal(t, []uint64{980}, f.pendingBlocks(t), "block 980 stays pending for the block scan's retry")
	assert.Equal(t, chain.SignatureQuery{Until: "sig-old", FloorSlot: 995, HasFloor: true, MaxPages: signatureHistoryPageCeiling}, f.chain.queriesOf(sigBob)[0])
}

func TestSync_Signatures_KeepsEveryPendingBlockWhileAnAccountFails(t *testing.T) {
	for name, fail := range map[string]func(f *signatureFixture){
		"an account's history":       func(f *signatureFixture) { f.chain.txErr["sig-1"] = errors.New("429") },
		"an address's accounts list": func(f *signatureFixture) { f.chain.accountErr[sigAlice] = errors.New("429") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignatureFixture(t)
			f.keepPending(t, 1050)
			f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
			fail(f)

			summary := f.sync(t)

			assert.Equal(t, 1, summary.FailedAccounts)
			assert.Zero(t, summary.ResolvedPending)
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
	sameCursor(t, cursorAt("sig-14", 14), f.cursors(t)[sigBob], 13)
}

func TestSync_Signatures_NeverCutsAHistoryAboveTheFloor(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.pageSize = 1
	for i := 0; i <= signatureHistoryPageCeiling; i++ {
		slot := uint64(sigCheckpoint + 1 + i)
		f.chain.history(sigBob, f.chain.tx(fmt.Sprintf("sig-%d", slot), slot, lamports(sigBob, 1, 1)))
	}

	summary := f.sync(t)

	assert.Equal(t, 1, summary.FailedAccounts, "older signatures could hold deposits: the account fails instead")
	assert.Empty(t, sigDeposits(t))
	_, hasCursor := f.cursors(t)[sigBob]
	assert.False(t, hasCursor)
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
	assert.Equal(t, "sig-gas_seed", f.cursors(t)[sigBob].Signature, "the cursor moves past what is not a deposit")
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

func TestSync_Signatures_HoldsTheChainsScanLockForTheWholeCycle(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.history(sigBob, f.chain.tx("sig-1", 1201, lamports(sigBob, 10, 1)))
	var mu sync.Mutex
	var blocked []error
	f.chain.duringRead = func() {
		_, err := f.svc.lockChain(sigChain)
		mu.Lock()
		defer mu.Unlock()
		blocked = append(blocked, err)
	}

	f.sync(t)

	require.NotEmpty(t, blocked)
	for _, err := range blocked {
		assert.ErrorIs(t, err, ErrScanInProgress, "a block scan of the chain cannot start during the sync")
	}
	release, err := f.svc.lockChain(sigChain)
	require.NoError(t, err, "the lock is released once the cycle ends")
	release()
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

func TestSync_Signatures_RecordsItsCycleForTheHealthCheck(t *testing.T) {
	f := newSignatureFixture(t)
	f.chain.accountErr[sigBob] = errors.New("429")
	activity := newMemoryActivity()
	f.svc.SetScanActivityStore(activity)
	f.svc.now = func() time.Time { return activityNow }

	f.sync(t)

	assert.Equal(t, SignatureSyncRecord{Accounts: 3, FailedAccounts: 1, At: activityNow}, activity.signatures[sigChain],
		"alice's two accounts and bob, whose accounts were not listed")
	readings := f.svc.ScanActivityHealth(context.Background())
	assert.True(t, readings.Chains[sigChain].SignatureMode())
}

func TestScan_LatestBlocks_PausesWhileTheSignatureSyncReadsTheChainAndResumesFromItsCheckpoint(t *testing.T) {
	logs := captureLogs(t)
	f := newSignatureFixture(t)
	scanned := 0
	f.chain.ScanBlockFn = func(context.Context, uint64) ([]types.DetectedTransfer, error) {
		scanned++
		return nil, nil
	}
	f.keepPending(t, 990)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, f.svc.ScanLatestBlocks(ctx, sigChain))
		resolved, err := f.svc.ReprocessDuePending(ctx, sigChain)
		require.NoError(t, err)
		assert.Zero(t, resolved)
	}

	assert.Zero(t, scanned, "no block is read while the chain is in signature mode")
	assert.Equal(t, []uint64{990}, f.pendingBlocks(t), "the pending retry waits too")
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

	assert.Equal(t, 5, scanned, "the block scan resumes from its checkpoint")
	checkpoint, err = f.svc.checkpoints.Find(ctx, sigChain)
	require.NoError(t, err)
	assert.Equal(t, uint64(sigCheckpoint+5), checkpoint)
	assert.Len(t, recordsOf(logs(), "INFO", "deposit block scan resumed from its checkpoint: the signature sync is off for the chain"), 1)
}

func TestScan_LatestBlocks_KeepsScanningAChainTheSettingsListButCannotSyncBySignatures(t *testing.T) {
	logs := captureLogs(t)
	f := newSignatureFixture(t)
	plain := mocks.NewMockChain(models.ChainETH)
	plain.GetLatestBlockFn = func(context.Context) (uint64, error) { return 100, nil }
	f.registry.RegisterChain(plain)
	f.settings.Chains = []string{models.ChainETH}

	require.NoError(t, f.svc.ScanLatestBlocks(context.Background(), models.ChainETH))
	require.NoError(t, f.svc.ScanLatestBlocks(context.Background(), models.ChainETH))

	checkpoint, err := f.svc.checkpoints.Find(context.Background(), models.ChainETH)
	require.NoError(t, err, "the first cycle starts the chain at the head")
	assert.Equal(t, uint64(100), checkpoint)
	assert.Len(t, recordsOf(logs(), "WARN", "deposit signature sync enabled for a chain whose adapter cannot read signatures; its block scan keeps running"), 1)
}

func TestScan_Alerts_AChainInSignatureModeAlertsOnItsSyncNotOnItsPausedScan(t *testing.T) {
	logs := captureLogs(t)
	clock := &testClock{now: activityNow}
	activity := newMemoryActivity()
	// sol's block scan stalled hours ago; its sync ran a minute ago and left an
	// account failed. tsol's sync stopped half an hour ago.
	activity.scans["sol"] = ScanRecord{Head: 1_000_000, Checkpoint: 775_000, At: activityNow.Add(-3 * time.Hour)}
	activity.signatures["sol"] = SignatureSyncRecord{Accounts: 36, FailedAccounts: 1, At: activityNow.Add(-time.Minute)}
	activity.signatures["eth"] = SignatureSyncRecord{Accounts: 2, At: activityNow.Add(-30 * time.Minute)}
	svc := activityService(activity, nil, clock)
	svc.txRepo = &sweepConfirmMemory{}
	svc.SetScanAlertState(newMemoryAlerts(clock))
	svc.signatureSettings = func(context.Context) SignatureSyncSettings { return SignatureSyncSettings{Interval: 10 * time.Minute} }

	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))

	records := logs()
	assert.Empty(t, warnings(records, scanLagMessage), "a paused block scan raises no lag alert")
	assert.Empty(t, warnings(records, scanStaleMessage), "nor a stale one")
	failedAccounts := warnings(records, signatureFailedMessage)
	require.Len(t, failedAccounts, 1)
	assert.Equal(t, float64(1), failedAccounts["sol"]["failed_accounts"])
	stale := warnings(records, signatureStaleMessage)
	require.Len(t, stale, 1, "only eth's sync is older than the stale threshold plus its interval (20 minutes)")
	assert.Equal(t, float64(20), stale["eth"]["threshold_minutes"])

	activity.scans["eth"] = ScanRecord{Head: 50, Checkpoint: 50, At: activityNow}
	require.NoError(t, svc.RunWithdrawalConfirmationCheck(context.Background()))
	assert.Len(t, recordsOf(logs(), "INFO", signatureStaleClearedMessage), 1, "a chain the block scan reads again clears its sync alerts")
}
