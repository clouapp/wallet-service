package deposit

import (
	"context"
	"math/big"
	"os"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/pkg/types"
	"github.com/macromarkets/vault/tests/mocks"
	"github.com/macromarkets/vault/tests/testutil"
)

func TestMain(m *testing.M) {
	// Boot Goravel once for all tests in this package
	testutil.BootTest()
	os.Exit(m.Run())
}

func setupDepositService(t *testing.T) (*Service, *mocks.MockChain, *mocks.MockSQS) {
	t.Helper()
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 3
	registry.RegisterChain(mockChain)

	mockSQS := mocks.NewMockSQS()
	webhookSvc := webhook.NewService(nil) // nil SQS — enqueue will skip send
	svc := NewService(nil, registry, webhookSvc)
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

	svc := NewService(nil, registry, nil)
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
	svc := NewService(nil, registry, nil)

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

	svc := NewService(nil, registry, webhook.NewService(nil))

	transfer := types.DetectedTransfer{
		TxHash: "0xdeposithash123", BlockNumber: 100, BlockHash: "0xblock",
		From: "0xsender", To: addr.Address,
		Amount: big.NewInt(1000000), Asset: "eth",
	}

	err := svc.processTransfer(context.Background(), "eth", mockChain, transfer)
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
}

func TestProcessTransfer_IgnoresUnknownAddress(t *testing.T) {
	mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	registry.RegisterChain(mockChain)

	svc := NewService(nil, registry, nil)

	transfer := types.DetectedTransfer{
		TxHash: "0xignored", To: "0xunknown_address", Amount: big.NewInt(100), Asset: "eth",
	}

	err := svc.processTransfer(context.Background(), "eth", mockChain, transfer)
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

	svc := NewService(nil, registry, webhook.NewService(nil))

	transfer := types.DetectedTransfer{
		TxHash: "0xsametx", BlockNumber: 100, To: addr.Address,
		Amount: big.NewInt(500), Asset: "eth",
	}

	// Process twice
	svc.processTransfer(context.Background(), "eth", mockChain, transfer)
	svc.processTransfer(context.Background(), "eth", mockChain, transfer)

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

	svc := NewService(nil, registry, webhook.NewService(nil))

	token := types.Token{Symbol: "usdt", Contract: "0xdAC17F", Decimals: 6, ChainID: "eth"}
	transfer := mocks.MakeTokenTransfer("0xtokentx", "0xfrom", addr.Address, 500000, token)

	err := svc.processTransfer(context.Background(), "eth", mockChain, transfer)
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

	svc := NewService(nil, registry, webhook.NewService(nil))

	// Current block = 101 → 1 conf → confirming
	svc.updateConfirmations(context.Background(), "eth", mockChain, 101)
	var tx models.Transaction
	if err := facades.Orm().Query().Find(&tx, insertedTx.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if tx.Status != "confirming" {
		t.Errorf("expected confirming, got %s", tx.Status)
	}

	// Current block = 103 → 3 confs → confirmed
	svc.updateConfirmations(context.Background(), "eth", mockChain, 103)
	if err := facades.Orm().Query().Find(&tx, insertedTx.ID); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	if tx.Status != "confirmed" {
		t.Errorf("expected confirmed, got %s", tx.Status)
	}
}
