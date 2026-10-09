package commands

import (
	"testing"

	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/refresh"
)

func TestNew_Refresh_CurrencyKeepsItsDependencies(t *testing.T) {
	registry := chainpkg.NewRegistry()
	balances := refresh.NewBalanceService(refresh.Deps{})
	cmd := NewRefreshCurrency(RefreshCurrencyDeps{
		Registry: registry,
		Balances: balances,
	})
	if cmd == nil {
		t.Fatal("NewRefreshCurrency returned nil")
	}
	if cmd.registry != registry {
		t.Fatal("refresh currency did not keep the chain registry")
	}
	if cmd.balances != balances {
		t.Fatal("refresh currency did not keep the balance service")
	}
}

func TestNew_Refresh_CurrencyRequiresARegistry(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:currency: chain registry is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshCurrency(RefreshCurrencyDeps{
		Balances: refresh.NewBalanceService(refresh.Deps{}),
	})
}

func TestNew_Refresh_CurrencyRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:currency: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshCurrency(RefreshCurrencyDeps{
		Registry: chainpkg.NewRegistry(),
	})
}
