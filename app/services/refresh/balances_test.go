package refresh

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestNewBalanceServiceNotNil(t *testing.T) {
	svc := NewBalanceService(nil, nil, nil, nil, nil)
	if svc == nil {
		t.Fatal("expected non-nil BalanceService")
	}
}

func TestNewBalanceServiceStoresAllDependencies(t *testing.T) {
	reg := chain.NewRegistry()
	svc := NewBalanceService(reg, nil, nil, nil, nil)
	if svc.registry != reg {
		t.Fatal("expected registry to be stored")
	}
	if svc.walletRepo != nil {
		t.Fatal("expected nil walletRepo")
	}
	if svc.assetBalanceRepo != nil {
		t.Fatal("expected nil assetBalanceRepo")
	}
	if svc.snapshotRepo != nil {
		t.Fatal("expected nil snapshotRepo")
	}
	if svc.syncStateRepo != nil {
		t.Fatal("expected nil syncStateRepo")
	}
}

func TestBalanceServiceRefreshWalletRejectsNilDepositAddress(t *testing.T) {
	reg := chain.NewRegistry()
	mock := mocks.NewMockChain("eth")
	reg.RegisterChain(mock)

	svc := NewBalanceService(reg, nil, nil, nil, nil)
	wallet := &models.Wallet{
		ID:    uuid.New(),
		Chain: "eth",
	}
	err := svc.RefreshWallet(context.Background(), wallet)
	if err == nil {
		t.Fatal("expected error for nil deposit address")
	}
	if !strings.Contains(err.Error(), "no deposit address") {
		t.Fatalf("expected 'no deposit address' error, got: %v", err)
	}
}

func TestBalanceServiceRefreshWalletRejectsUnknownChain(t *testing.T) {
	reg := chain.NewRegistry()
	svc := NewBalanceService(reg, nil, nil, nil, nil)
	wallet := &models.Wallet{
		ID:    uuid.New(),
		Chain: "nonexistent",
	}
	err := svc.RefreshWallet(context.Background(), wallet)
	if err == nil {
		t.Fatal("expected error for unknown chain")
	}
	if !strings.Contains(err.Error(), "chain adapter not found") {
		t.Fatalf("expected 'chain adapter not found' error, got: %v", err)
	}
}
