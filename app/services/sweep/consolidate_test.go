package sweep

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestConsolidateAll_ShortPassphraseRejected verifies the input guard fires
// before any repository / lock / quota work, so a bad call cannot burn the
// per-account daily quota.
func TestConsolidateAll_ShortPassphraseRejected(t *testing.T) {
	svc := &service{}
	_, err := svc.ConsolidateAll(context.Background(), uuid.New(), "usdt", "short")
	if err == nil {
		t.Fatal("expected error for short passphrase, got nil")
	}
	if err.Error() != "passphrase must be at least 12 characters" {
		t.Fatalf("expected passphrase-length error, got %v", err)
	}
}

// TestConsolidateAll_NonEVMChainRejected locks in the v1 EVM-only guard.
// Non-EVM wallets must surface ErrUnsupportedChain before any lock / quota
// side-effects are taken.
func TestConsolidateAll_NonEVMChainRejected(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	wallet := &models.Wallet{ID: walletID, Chain: "btc", DepositAddress: &baseAddr}
	chainEntity := &models.Chain{ID: "btc", AdapterType: models.AdapterTypeBitcoin}

	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("btc"))
	svc := &service{
		registry:   registry,
		walletRepo: &fakeWalletRepo{wallet: wallet},
		chainRepo:  &fakeChainRepo{chain: chainEntity},
	}

	_, err := svc.ConsolidateAll(context.Background(), walletID, "btc", "passphrase12345")
	if err != ErrUnsupportedChain {
		t.Fatalf("expected ErrUnsupportedChain, got %v", err)
	}
}

// TestConsolidateAll_NoEligibleChildren_Noop exercises the early-return path:
// the wallet only has its base address, so there is nothing to sweep and the
// function returns an empty Result without touching the MPC / signing
// pipeline. This also proves the call is safe to poll — back-to-back noops
// are cheap and allocate no rows.
func TestConsolidateAll_NoEligibleChildren_Noop(t *testing.T) {
	walletID := uuid.New()
	baseAddr := models.Address{ID: uuid.New(), WalletID: walletID, Address: "BASE"}
	wallet := &models.Wallet{
		ID:             walletID,
		Chain:          "eth",
		DepositAddress: &baseAddr,
		MPCCurve:       "secp256k1",
	}
	chainEntity := &models.Chain{ID: "eth", AdapterType: models.AdapterTypeEVM}

	mockChain := mocks.NewMockChain("eth")
	mockChain.NativeAssetVal = "eth"
	mockChain.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		return &types.Balance{Address: addr, Asset: "eth", Amount: big.NewInt(0)}, nil
	}

	registry := chain.NewRegistry()
	registry.RegisterChain(mockChain)

	txRepo := &fakeTxRepo{}
	svc := &service{
		registry:    registry,
		walletRepo:  &fakeWalletRepo{wallet: wallet},
		addressRepo: &fakeAddressRepo{children: []models.Address{baseAddr}},
		chainRepo:   &fakeChainRepo{chain: chainEntity},
		accountRepo: &fakeAccountRepo{},
		txRepo:      txRepo,
	}

	res, err := svc.ConsolidateAll(context.Background(), walletID, "eth", "passphrase12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil Result on noop")
	}
	if len(res.Sweeps) != 0 {
		t.Fatalf("expected 0 sweeps on noop, got %d", len(res.Sweeps))
	}
	if res.FailedStep != nil {
		t.Fatalf("expected nil FailedStep, got %+v", res.FailedStep)
	}
	if res.FinalWithdrawTx != nil {
		t.Fatalf("expected no final withdrawal tx for consolidate, got %+v", res.FinalWithdrawTx)
	}
	if len(txRepo.created) != 0 {
		t.Fatalf("expected 0 persisted txs on noop, got %d", len(txRepo.created))
	}
}
