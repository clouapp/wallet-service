package wallet

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/macromarkets/vault/app/services/chain"
	"github.com/macromarkets/vault/tests/mocks"
)

func setupWalletService(t *testing.T) (*Service, *chain.Registry) {
	t.Helper()
	db := mocks.TestDB(t)
	registry := chain.NewRegistry()
	registry.RegisterChain(mocks.NewMockChain("eth"))
	registry.RegisterChain(mocks.NewMockChain("btc"))
	registry.RegisterChain(mocks.NewMockChain("sol"))
	svc := NewService(db, registry, nil) // no redis in tests
	return svc, registry
}

func TestCreateWallet(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	w, err := svc.CreateWallet(ctx, "eth", "Ethereum Wallet")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if w.Chain != "eth" {
		t.Errorf("expected chain=eth, got %s", w.Chain)
	}
	if w.Label != "Ethereum Wallet" {
		t.Errorf("expected label, got %s", w.Label)
	}
	if w.AddressIndex != 0 {
		t.Errorf("expected index=0, got %d", w.AddressIndex)
	}
	if w.DerivationPath != "m/44'/60'/0'/0" {
		t.Errorf("expected EVM derivation path, got %s", w.DerivationPath)
	}
}

func TestCreateWallet_DuplicateChain(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	svc.CreateWallet(ctx, "eth", "first")
	_, err := svc.CreateWallet(ctx, "eth", "second")
	if err == nil {
		t.Fatal("expected error for duplicate chain wallet")
	}
}

func TestCreateWallet_UnknownChain(t *testing.T) {
	svc, _ := setupWalletService(t)
	_, err := svc.CreateWallet(context.Background(), "dogecoin", "Doge")
	if err == nil {
		t.Fatal("expected error for unknown chain")
	}
}

func TestListWallets(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	svc.CreateWallet(ctx, "eth", "ETH")
	svc.CreateWallet(ctx, "btc", "BTC")

	wallets, err := svc.ListWallets(ctx)
	if err != nil {
		t.Fatalf("ListWallets: %v", err)
	}
	if len(wallets) != 2 {
		t.Errorf("expected 2 wallets, got %d", len(wallets))
	}
}

func TestGetWallet(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	created, _ := svc.CreateWallet(ctx, "eth", "ETH")
	got, err := svc.GetWallet(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch")
	}
}

func TestGetWallet_NotFound(t *testing.T) {
	svc, _ := setupWalletService(t)
	_, err := svc.GetWallet(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error for nonexistent wallet")
	}
}

func TestGenerateAddress(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "eth", "ETH")

	addr, err := svc.GenerateAddress(ctx, w.ID, "user_123", `{"tier":"premium"}`)
	if err != nil {
		t.Fatalf("GenerateAddress: %v", err)
	}
	if addr.ExternalUserID != "user_123" {
		t.Errorf("expected user_123, got %s", addr.ExternalUserID)
	}
	if addr.DerivationIndex != 0 {
		t.Errorf("expected index=0, got %d", addr.DerivationIndex)
	}
	if addr.Chain != "eth" {
		t.Errorf("expected chain=eth, got %s", addr.Chain)
	}
	if addr.Address == "" {
		t.Error("expected non-empty address")
	}
}

func TestGenerateAddress_IncrementsIndex(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "eth", "ETH")

	addr1, _ := svc.GenerateAddress(ctx, w.ID, "user_1", "")
	addr2, _ := svc.GenerateAddress(ctx, w.ID, "user_2", "")
	addr3, _ := svc.GenerateAddress(ctx, w.ID, "user_3", "")

	if addr1.DerivationIndex != 0 { t.Errorf("expected 0, got %d", addr1.DerivationIndex) }
	if addr2.DerivationIndex != 1 { t.Errorf("expected 1, got %d", addr2.DerivationIndex) }
	if addr3.DerivationIndex != 2 { t.Errorf("expected 2, got %d", addr3.DerivationIndex) }

	// Verify wallet index updated
	updated, _ := svc.GetWallet(ctx, w.ID)
	if updated.AddressIndex != 3 {
		t.Errorf("expected wallet index=3, got %d", updated.AddressIndex)
	}
}

func TestGenerateAddress_UniqueAddresses(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "eth", "ETH")
	addr1, _ := svc.GenerateAddress(ctx, w.ID, "user_1", "")
	addr2, _ := svc.GenerateAddress(ctx, w.ID, "user_2", "")

	if addr1.Address == addr2.Address {
		t.Error("addresses should be unique per user")
	}
}

func TestLookupAddress(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "eth", "ETH")
	created, _ := svc.GenerateAddress(ctx, w.ID, "user_abc", "")

	found, err := svc.LookupAddress(ctx, "eth", created.Address)
	if err != nil {
		t.Fatalf("LookupAddress: %v", err)
	}
	if found.ExternalUserID != "user_abc" {
		t.Errorf("expected user_abc, got %s", found.ExternalUserID)
	}
}

func TestLookupAddress_NotFound(t *testing.T) {
	svc, _ := setupWalletService(t)
	_, err := svc.LookupAddress(context.Background(), "eth", "0xnonexistent")
	if err == nil {
		t.Fatal("expected error for unknown address")
	}
}

func TestListUserAddresses(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	wEth, _ := svc.CreateWallet(ctx, "eth", "ETH")
	wBtc, _ := svc.CreateWallet(ctx, "btc", "BTC")

	svc.GenerateAddress(ctx, wEth.ID, "user_multi", "")
	svc.GenerateAddress(ctx, wBtc.ID, "user_multi", "")
	svc.GenerateAddress(ctx, wEth.ID, "user_other", "")

	addrs, err := svc.ListUserAddresses(ctx, "user_multi")
	if err != nil {
		t.Fatalf("ListUserAddresses: %v", err)
	}
	if len(addrs) != 2 {
		t.Errorf("expected 2 addresses for user_multi, got %d", len(addrs))
	}
}

func TestListWalletAddresses(t *testing.T) {
	svc, _ := setupWalletService(t)
	ctx := context.Background()

	w, _ := svc.CreateWallet(ctx, "eth", "ETH")
	svc.GenerateAddress(ctx, w.ID, "user_1", "")
	svc.GenerateAddress(ctx, w.ID, "user_2", "")

	addrs, err := svc.ListWalletAddresses(ctx, w.ID)
	if err != nil {
		t.Fatalf("ListWalletAddresses: %v", err)
	}
	if len(addrs) != 2 {
		t.Errorf("expected 2, got %d", len(addrs))
	}
}

func TestDerivationPath(t *testing.T) {
	tests := []struct {
		chain, want string
	}{
		{"btc", "m/84'/0'/0'/0"},
		{"eth", "m/44'/60'/0'/0"},
		{"polygon", "m/44'/60'/0'/0"},
		{"sol", "m/44'/501'"},
		{"unknown", "m/44'/0'/0'/0"},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			if got := derivationPath(tt.chain); got != tt.want {
				t.Errorf("derivationPath(%s) = %s, want %s", tt.chain, got, tt.want)
			}
		})
	}
}
