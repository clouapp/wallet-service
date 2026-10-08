package commands

import (
	"testing"

	"github.com/macrowallets/waas/app/services/refresh"
)

func TestNew_Refresh_WalletKeepsItsDependencies(t *testing.T) {
	balances := refresh.NewBalanceService(refresh.Deps{})
	cmd := NewRefreshWallet(RefreshWalletDeps{
		Balances: balances,
	})
	if cmd == nil {
		t.Fatal("NewRefreshWallet returned nil")
	}
	if cmd.balances != balances {
		t.Fatal("refresh wallet did not keep the balance service")
	}
}

func TestNew_Refresh_WalletRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:wallet: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshWallet(RefreshWalletDeps{})
}
