package commands

import (
	"testing"

	"github.com/macrowallets/waas/app/services/refresh"
)

func TestNew_Refresh_AddressKeepsItsDependencies(t *testing.T) {
	balances := refresh.NewBalanceService(refresh.Deps{})
	cmd := NewRefreshAddress(RefreshAddressDeps{
		Balances: balances,
	})
	if cmd == nil {
		t.Fatal("NewRefreshAddress returned nil")
	}
	if cmd.balances != balances {
		t.Fatal("refresh address did not keep the balance service")
	}
}

func TestNew_Refresh_AddressRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:address: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshAddress(RefreshAddressDeps{})
}
