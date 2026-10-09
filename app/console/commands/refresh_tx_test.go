package commands

import (
	"testing"

	"github.com/macrowallets/waas/app/services/refresh"
)

func TestNew_Refresh_TxKeepsItsDependencies(t *testing.T) {
	balances := refresh.NewBalanceService(refresh.Deps{})
	cmd := NewRefreshTx(RefreshTxDeps{
		Balances: balances,
	})
	if cmd == nil {
		t.Fatal("NewRefreshTx returned nil")
	}
	if cmd.balances != balances {
		t.Fatal("refresh tx did not keep the balance service")
	}
}

func TestNew_Refresh_TxRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:tx: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshTx(RefreshTxDeps{})
}
