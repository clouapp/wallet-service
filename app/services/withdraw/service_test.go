package withdraw

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/app/services/queue"
	"github.com/macromarkets/vault/app/services/webhook"
	"github.com/macromarkets/vault/pkg/types"
	"github.com/macromarkets/vault/tests/mocks"
)

func setupWithdrawService(t *testing.T) (*Service, *mocks.MockChain) {
	t.Helper()
	db := mocks.TestDB(t)
	registry := chain.NewRegistry()
	mockChain := mocks.NewMockChain("eth")
	mockChain.RequiredConfirmationsVal = 12
	registry.RegisterChain(mockChain)
	registry.RegisterToken(types.Token{Symbol: "usdt", ChainID: "eth", Decimals: 6, Contract: "0xdAC17F"})

	webhookSvc := webhook.NewService(db, nil)
	sqsClient := &queue.SQSClient{} // nil inner client — send will be no-op
	svc := NewService(db, registry, sqsClient, webhookSvc)
	return svc, mockChain
}

func TestRequest_Success(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	db := svc.db
	ctx := context.Background()

	w := mocks.InsertWallet(t, db, "eth")

	tx, err := svc.Request(ctx, WithdrawRequest{
		WalletID:       w.ID,
		ExternalUserID: "user_123",
		ToAddress:      "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
		Amount:         "1000000",
		Asset:          "eth",
		IdempotencyKey: "withdraw_001",
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if tx.Status != "pending" {
		t.Errorf("expected pending, got %s", tx.Status)
	}
	if tx.ExternalUserID != "user_123" {
		t.Errorf("expected user_123, got %s", tx.ExternalUserID)
	}
	if tx.Chain != "eth" {
		t.Errorf("expected eth, got %s", tx.Chain)
	}
	if tx.Asset != "eth" {
		t.Errorf("expected eth asset, got %s", tx.Asset)
	}
}

func TestRequest_Idempotency(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	db := svc.db
	ctx := context.Background()

	w := mocks.InsertWallet(t, db, "eth")

	req := WithdrawRequest{
		WalletID:       w.ID,
		ExternalUserID: "user_123",
		ToAddress:      "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
		Amount:         "500000",
		Asset:          "eth",
		IdempotencyKey: "idem_001",
	}

	tx1, err := svc.Request(ctx, req)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	tx2, err := svc.Request(ctx, req)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}

	if tx1.ID != tx2.ID {
		t.Error("idempotent requests should return the same transaction")
	}
}

func TestRequest_InvalidAddress(t *testing.T) {
	svc, mock := setupWithdrawService(t)
	db := svc.db
	ctx := context.Background()

	mock.ValidateAddressFn = func(address string) bool { return false }

	w := mocks.InsertWallet(t, db, "eth")

	_, err := svc.Request(ctx, WithdrawRequest{
		WalletID:       w.ID,
		ExternalUserID: "user_123",
		ToAddress:      "invalid_addr",
		Amount:         "100",
		Asset:          "eth",
		IdempotencyKey: "inv_001",
	})
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestRequest_WalletNotFound(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	_, err := svc.Request(context.Background(), WithdrawRequest{
		WalletID:       uuid.New(),
		ToAddress:      "0x123",
		Amount:         "100",
		Asset:          "eth",
		IdempotencyKey: "notfound_001",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent wallet")
	}
}

func TestRequest_TokenWithdrawal(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	db := svc.db
	ctx := context.Background()

	w := mocks.InsertWallet(t, db, "eth")

	tx, err := svc.Request(ctx, WithdrawRequest{
		WalletID:       w.ID,
		ExternalUserID: "user_token",
		ToAddress:      "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
		Amount:         "1000000",
		Asset:          "usdt",
		IdempotencyKey: "token_001",
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if tx.Asset != "usdt" {
		t.Errorf("expected usdt, got %s", tx.Asset)
	}
	if !tx.TokenContract.Valid {
		t.Error("expected token_contract to be set for USDT withdrawal")
	}
}

func TestRequest_UnknownToken(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	db := svc.db
	ctx := context.Background()

	w := mocks.InsertWallet(t, db, "eth")

	_, err := svc.Request(ctx, WithdrawRequest{
		WalletID:       w.ID,
		ExternalUserID: "user_123",
		ToAddress:      "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
		Amount:         "100",
		Asset:          "dogecoin",
		IdempotencyKey: "unk_001",
	})
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
}

func TestGetTransaction(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	db := svc.db
	ctx := context.Background()

	w := mocks.InsertWallet(t, db, "eth")
	inserted := mocks.InsertTransaction(t, db, w.ID, nil, "eth", "withdrawal", "pending", "eth", "100", 0)

	got, err := svc.GetTransaction(ctx, inserted.ID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if got.ID != inserted.ID {
		t.Error("ID mismatch")
	}
}

func TestGetTransaction_NotFound(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	_, err := svc.GetTransaction(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListTransactions_Filters(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	db := svc.db
	ctx := context.Background()

	w := mocks.InsertWallet(t, db, "eth")
	mocks.InsertTransaction(t, db, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)
	mocks.InsertTransaction(t, db, w.ID, nil, "eth", "withdrawal", "pending", "usdt", "200", 0)
	mocks.InsertTransaction(t, db, w.ID, nil, "eth", "deposit", "pending", "eth", "300", 60)

	// All
	all, _ := svc.ListTransactions(ctx, "", "", "", "", 50, 0)
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	// Filter by type
	deposits, _ := svc.ListTransactions(ctx, "", "deposit", "", "", 50, 0)
	if len(deposits) != 2 {
		t.Errorf("expected 2 deposits, got %d", len(deposits))
	}

	// Filter by status
	pending, _ := svc.ListTransactions(ctx, "", "", "pending", "", 50, 0)
	if len(pending) != 2 {
		t.Errorf("expected 2 pending, got %d", len(pending))
	}

	// Filter by chain
	eth, _ := svc.ListTransactions(ctx, "eth", "", "", "", 50, 0)
	if len(eth) != 3 {
		t.Errorf("expected 3 eth txs, got %d", len(eth))
	}

	// Limit
	limited, _ := svc.ListTransactions(ctx, "", "", "", "", 1, 0)
	if len(limited) != 1 {
		t.Errorf("expected 1 with limit, got %d", len(limited))
	}
}

func TestExecute_Idempotency(t *testing.T) {
	svc, _ := setupWithdrawService(t)
	db := svc.db
	ctx := context.Background()

	w := mocks.InsertWallet(t, db, "eth")
	tx := mocks.InsertTransaction(t, db, w.ID, nil, "eth", "withdrawal", "confirmed", "eth", "100", 50)

	// Should skip already-confirmed transaction
	err := svc.Execute(ctx, types.WithdrawalMessage{
		TransactionID: tx.ID.String(), WalletID: w.ID.String(),
		ChainID: "eth", ToAddress: "0xto", Amount: "100", Asset: "eth",
	})
	if err != nil {
		t.Fatalf("Execute on confirmed tx should succeed (skip): %v", err)
	}
}
