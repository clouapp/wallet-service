package deposit

import (
	"context"
	"math/big"
	"os"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/macrowallets/waas/tests/testutil"
)

func TestMain(m *testing.M) {
	// Boot Goravel once for all tests in this package
	testutil.BootTest()
	os.Exit(m.Run())
}

func newWebhookSvc() *webhook.Service {
	return webhook.NewService(nil, repositories.NewWebhookConfigRepository(nil, facades.Crypt()), repositories.NewWebhookEventRepository(nil))
}

func newDepositSvc(registry *chain.Registry, webhookSvc *webhook.Service) *Service {
	return NewService(nil, registry, webhookSvc, repositories.NewAddressRepository(nil), repositories.NewTransactionRepository(nil), nil)
}

func setupDepositService(t *testing.T) (*Service, *mocks.MockChain, *mocks.MockSQS) {
	t.Helper()
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	registry.RegisterChain(mockChain)

	mockSQS := mocks.NewMockSQS()
	svc := newDepositSvc(registry, newWebhookSvc())
	return svc, mockChain, mockSQS
}

// We test the core logic without a running blockchain — mock the adapter.
func TestScanLatestBlocks_NoNewBlocks(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.GetLatestBlockFn = func(ctx context.Context) (uint64, error) {
		return 100, nil
	}
	registry.RegisterChain(mockChain)

	svc := newDepositSvc(registry, nil)
	err := svc.ScanLatestBlocks(context.Background(), "eth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// First run sets checkpoint, second run should find no new blocks
	err = svc.ScanLatestBlocks(context.Background(), "eth")
	if err != nil {
		t.Fatalf("unexpected error on second scan: %v", err)
	}
}

func TestScanLatestBlocks_UnknownChain(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	svc := newDepositSvc(registry, nil)

	err := svc.ScanLatestBlocks(context.Background(), "dogecoin")
	if err == nil {
		t.Fatal("expected error for unknown chain")
	}
}

func TestProcessTransfer_MatchesAddress(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	registry.RegisterChain(mockChain)

	// Insert a wallet + address
	w := mocks.InsertWallet(t, "eth")
	addr := mocks.InsertAddress(t, w.ID, "eth", "0xuser_deposit_addr", "user_123", 0)

	svc := newDepositSvc(registry, newWebhookSvc())

	transfer := types.DetectedTransfer{
		TxHash: "0xdeposithash123", BlockNumber: 100, BlockHash: "0xblock",
		From: "0xsender", To: addr.Address,
		Amount: big.NewInt(1000000), Asset: "eth",
	}

	_, err := svc.processTransfer(context.Background(), "eth", mockChain, transfer)
	if err != nil {
		t.Fatalf("processTransfer: %v", err)
	}

	// Verify transaction was created
	count, err := facades.Orm().Query().Model(&models.Transaction{}).Where("tx_hash", "0xdeposithash123").Count()
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 transaction, got %d", count)
	}

	// Verify correct user mapping
	var tx models.Transaction
	if err := facades.Orm().Query().Where("tx_hash", "0xdeposithash123").First(&tx); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if tx.ExternalUserID != "user_123" {
		t.Errorf("expected user_123, got %s", tx.ExternalUserID)
	}
	if tx.Direction != models.TxDirectionInbound {
		t.Errorf("expected inbound direction, got %q", tx.Direction)
	}
	if tx.Source != models.TxSourceChain {
		t.Errorf("expected chain source, got %q", tx.Source)
	}
	if tx.RawPayload != "{}" {
		t.Errorf("expected valid empty raw payload, got %q", tx.RawPayload)
	}
}

func TestProcessTransfer_IgnoresUnknownAddress(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	registry.RegisterChain(mockChain)

	svc := newDepositSvc(registry, nil)

	transfer := types.DetectedTransfer{
		TxHash: "0xignored", To: "0xunknown_address", Amount: big.NewInt(100), Asset: "eth",
	}

	_, err := svc.processTransfer(context.Background(), "eth", mockChain, transfer)
	if err != nil {
		t.Fatalf("should not error for unknown address: %v", err)
	}

	count, err := facades.Orm().Query().Model(&models.Transaction{}).Where("tx_hash", "0xignored").Count()
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("should not have created transaction for unknown address")
	}
}

func TestProcessTransfer_Dedup(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	registry.RegisterChain(mockChain)

	w := mocks.InsertWallet(t, "eth")
	addr := mocks.InsertAddress(t, w.ID, "eth", "0xdedup_addr", "user_dedup", 0)

	svc := newDepositSvc(registry, newWebhookSvc())

	transfer := types.DetectedTransfer{
		TxHash: "0xsametx", BlockNumber: 100, To: addr.Address,
		Amount: big.NewInt(500), Asset: "eth",
	}

	// Process twice
	created, err := svc.processTransfer(context.Background(), "eth", mockChain, transfer)
	if err != nil || !created {
		t.Fatalf("first pass must record the deposit, got created=%v err=%v", created, err)
	}
	created, err = svc.processTransfer(context.Background(), "eth", mockChain, transfer)
	if err != nil || created {
		t.Fatalf("second pass must not record it again, got created=%v err=%v", created, err)
	}

	count, err := facades.Orm().Query().Model(&models.Transaction{}).Where("tx_hash", "0xsametx").Where("chain", "eth").Count()
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 transaction (dedup), got %d", count)
	}
}

func TestProcessTransfer_TokenDeposit(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 12
	registry.RegisterChain(mockChain)

	w := mocks.InsertWallet(t, "eth")
	addr := mocks.InsertAddress(t, w.ID, "eth", "0xtoken_addr", "user_token", 0)

	svc := newDepositSvc(registry, newWebhookSvc())

	token := types.Token{Symbol: "usdt", Contract: "0xdAC17F", Decimals: 6, ChainID: "eth"}
	transfer := mocks.MakeTokenTransfer("0xtokentx", "0xfrom", addr.Address, 500000, token)

	_, err := svc.processTransfer(context.Background(), "eth", mockChain, transfer)
	if err != nil {
		t.Fatalf("processTransfer: %v", err)
	}

	var tx models.Transaction
	if err := facades.Orm().Query().Where("tx_hash", "0xtokentx").First(&tx); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if tx.Asset != "usdt" {
		t.Errorf("expected usdt, got %s", tx.Asset)
	}
}

func TestUpdateConfirmations(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	registry.RegisterChain(mockChain)

	w := mocks.InsertWallet(t, "eth")

	// Insert a pending deposit at block 100
	insertedTx := mocks.InsertTransaction(t, w.ID, nil, "eth", "deposit", "pending", "eth", "1000", 100)

	svc := newDepositSvc(registry, newWebhookSvc())

	// Current block = 101 → blocks 100 and 101 → 2 confs → confirming
	svc.updateConfirmations(context.Background(), "eth", mockChain, 101)
	var tx models.Transaction
	if err := facades.Orm().Query().Find(&tx, insertedTx.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if tx.Status != "confirming" || tx.Confirmations != 2 {
		t.Errorf("expected confirming with 2 confs, got %s with %d", tx.Status, tx.Confirmations)
	}

	// Current block = 102 → 3 confs → confirmed
	svc.updateConfirmations(context.Background(), "eth", mockChain, 102)
	if err := facades.Orm().Query().Find(&tx, insertedTx.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if tx.Status != "confirmed" || tx.Confirmations != 3 {
		t.Errorf("expected confirmed with 3 confs, got %s with %d", tx.Status, tx.Confirmations)
	}
}

// TestUpdateConfirmations_TipBlockCountsAsOne covers the off-by-one: a transaction in
// the tip block has one confirmation, so a chain requiring 1 confirms it right away.
func TestUpdateConfirmations_TipBlockCountsAsOne(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("btc")
	mockChain.RequiredConfirmationsVal = 1
	registry.RegisterChain(mockChain)

	w := mocks.InsertWallet(t, "btc")
	insertedTx := mocks.InsertTransaction(t, w.ID, nil, "btc", "deposit", "pending", "btc", "15000", 154745)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id", insertedTx.ID).
		Update(map[string]interface{}{"required_confs": 1}); err != nil {
		t.Fatal(err)
	}
	svc := newDepositSvc(registry, newWebhookSvc())

	svc.updateConfirmations(context.Background(), "btc", mockChain, 154745)

	var tx models.Transaction
	if err := facades.Orm().Query().Find(&tx, insertedTx.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if tx.Status != "confirmed" || tx.Confirmations != 1 || tx.ConfirmedAt == nil {
		t.Fatalf("expected confirmed with 1 conf in the tip block, got %s with %d", tx.Status, tx.Confirmations)
	}
}

// TestUpdateConfirmations_TipBehindTransactionCountsZero covers a lagging height
// provider that reports a tip below the transaction's block.
func TestUpdateConfirmations_TipBehindTransactionCountsZero(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	registry.RegisterChain(mockChain)

	w := mocks.InsertWallet(t, "eth")
	insertedTx := mocks.InsertTransaction(t, w.ID, nil, "eth", "deposit", "pending", "eth", "1000", 100)
	svc := newDepositSvc(registry, newWebhookSvc())

	svc.updateConfirmations(context.Background(), "eth", mockChain, 99)

	var tx models.Transaction
	if err := facades.Orm().Query().Find(&tx, insertedTx.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if tx.Status == "confirmed" || tx.Confirmations != 0 {
		t.Fatalf("expected 0 confs while the tip is behind, got %s with %d", tx.Status, tx.Confirmations)
	}
}

func TestConfirmationsAt(t *testing.T) {
	cases := []struct {
		name    string
		tip     uint64
		txBlock int64
		want    int
	}{
		{name: "tip block", tip: 100, txBlock: 100, want: 1},
		{name: "two blocks deep", tip: 101, txBlock: 100, want: 2},
		{name: "tip behind", tip: 99, txBlock: 100, want: 0},
		{name: "unknown block", tip: 100, txBlock: 0, want: 0},
		{name: "negative block", tip: 100, txBlock: -1, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := confirmationsAt(tc.tip, tc.txBlock); got != tc.want {
				t.Fatalf("confirmationsAt(%d, %d) = %d, want %d", tc.tip, tc.txBlock, got, tc.want)
			}
		})
	}
}

// TestUpdateConfirmations_ReconcilesOutboundBlockNumber covers Fix C2: sweep /
// withdrawal / gas_seed rows are inserted with block_number=0 because the
// executor only knows the tx hash at broadcast time. The confirmation loop
// must call adapter.GetTransactionBlock to backfill the block number before
// running confirmation math; otherwise these rows stay at `confirming`
// forever.
func TestUpdateConfirmations_ReconcilesOutboundBlockNumber(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	registry.RegisterChain(mockChain)

	w := mocks.InsertWallet(t, "eth")
	sweepTx := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeSweep, "confirming", "eth", "1000", 0)

	mockChain.GetTransactionBlockVal = 100
	var lookedUp string
	mockChain.GetTransactionBlockFn = func(ctx context.Context, hash string) (uint64, error) {
		lookedUp = hash
		return 100, nil
	}

	svc := newDepositSvc(registry, newWebhookSvc())

	// currentBlock=101 → confs=2 → still confirming after reconcile.
	svc.updateConfirmations(context.Background(), "eth", mockChain, 101)

	if lookedUp != sweepTx.TxHash {
		t.Fatalf("expected adapter lookup for %s, got %q", sweepTx.TxHash, lookedUp)
	}
	var reloaded models.Transaction
	if err := facades.Orm().Query().Find(&reloaded, sweepTx.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if reloaded.BlockNumber != 100 {
		t.Fatalf("expected block_number backfilled to 100, got %d", reloaded.BlockNumber)
	}
	if reloaded.Status != "confirming" {
		t.Fatalf("expected confirming at 2 confs, got %s", reloaded.Status)
	}

	// currentBlock=102 → 3 confs → confirmed; adapter is NOT called again
	// because block_number is now persisted.
	lookedUp = ""
	svc.updateConfirmations(context.Background(), "eth", mockChain, 102)
	if lookedUp != "" {
		t.Fatalf("expected adapter not to be re-queried, got lookup for %q", lookedUp)
	}
	if err := facades.Orm().Query().Find(&reloaded, sweepTx.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if reloaded.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %s", reloaded.Status)
	}
}

type recordingWithdrawalConfirmations struct {
	confirmed      []models.Transaction
	backfillCalls  int
	backfillLimits []int
}

func (r *recordingWithdrawalConfirmations) MarkConfirmed(_ context.Context, tx *models.Transaction) error {
	r.confirmed = append(r.confirmed, *tx)
	return nil
}

func (r *recordingWithdrawalConfirmations) Backfill(_ context.Context, limit int) (int, error) {
	r.backfillCalls++
	r.backfillLimits = append(r.backfillLimits, limit)
	return 0, nil
}

// TestRunWithdrawalConfirmationCheck_OnlyAdvancesWithdrawals covers the local
// tracker: the withdrawal reaches its required confirmations and is handed to
// the withdrawal publisher, while a deposit on the same chain is left untouched
// so no deposit webhook is emitted from a dev machine.
func TestRunWithdrawalConfirmationCheck_OnlyAdvancesWithdrawals(t *testing.T) {
	mocks.TestDB(t)
	const (
		withdrawalBlock = 100
		chainTip        = 103
	)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	mockChain.GetLatestBlockFn = func(ctx context.Context) (uint64, error) {
		return chainTip, nil
	}
	mockChain.GetTransactionBlockVal = withdrawalBlock
	registry.RegisterChain(mockChain)

	w := mocks.InsertWallet(t, "eth")
	withdrawalTx := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", 0)
	depositTx := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeDeposit, "pending", "eth", "1000", withdrawalBlock)

	recorder := &recordingWithdrawalConfirmations{}
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(recorder)

	if err := svc.RunWithdrawalConfirmationCheck(context.Background()); err != nil {
		t.Fatalf("RunWithdrawalConfirmationCheck: %v", err)
	}

	if len(recorder.confirmed) != 1 || recorder.confirmed[0].ID != withdrawalTx.ID {
		t.Fatalf("expected the withdrawal to be published once, got %+v", recorder.confirmed)
	}
	published := recorder.confirmed[0]
	if published.Status != string(types.TxStatusConfirmed) || published.Confirmations != chainTip-withdrawalBlock+1 {
		t.Fatalf("published tx status=%s confirmations=%d", published.Status, published.Confirmations)
	}
	if recorder.backfillCalls != 1 || recorder.backfillLimits[0] != withdrawalBackfillBatchSize {
		t.Fatalf("expected one backfill with limit %d, got %v", withdrawalBackfillBatchSize, recorder.backfillLimits)
	}

	var reloadedDeposit models.Transaction
	if err := facades.Orm().Query().Find(&reloadedDeposit, depositTx.ID); err != nil {
		t.Fatalf("find deposit: %v", err)
	}
	if reloadedDeposit.Status != "pending" || reloadedDeposit.Confirmations != 0 {
		t.Fatalf("deposit must be untouched, got status=%s confirmations=%d", reloadedDeposit.Status, reloadedDeposit.Confirmations)
	}
}

// TestUpdateConfirmations_StillPendingOutboundSkipped ensures that when the
// adapter reports block=0 (tx still in mempool) the row is left alone —
// block_number stays 0, status unchanged — so the next tick retries. This
// prevents us from flipping a pending tx to confirmed with a zero block.
func TestUpdateConfirmations_StillPendingOutboundSkipped(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	registry.RegisterChain(mockChain)

	w := mocks.InsertWallet(t, "eth")
	withdrawal := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", 0)

	mockChain.GetTransactionBlockVal = 0

	svc := newDepositSvc(registry, newWebhookSvc())
	svc.updateConfirmations(context.Background(), "eth", mockChain, 500)

	var reloaded models.Transaction
	if err := facades.Orm().Query().Find(&reloaded, withdrawal.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if reloaded.BlockNumber != 0 {
		t.Fatalf("expected block_number still 0, got %d", reloaded.BlockNumber)
	}
	if reloaded.Status != "confirming" {
		t.Fatalf("expected status still confirming, got %s", reloaded.Status)
	}
}
