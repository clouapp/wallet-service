package commands

import (
	"testing"

	"github.com/macrowallets/waas/app/services/refresh"
)

func TestNew_Refresh_TxKeepsItsDependencies(t *testing.T) {
	balances := refresh.NewBalanceService(refresh.Deps{})
	dispatcher := &refreshAddressDispatcherStub{}
	cmd := NewRefreshTx(RefreshTxDeps{
		Balances:   balances,
		Dispatcher: dispatcher,
	})
	if cmd == nil {
		t.Fatal("NewRefreshTx returned nil")
	}
	if cmd.balances != balances {
		t.Fatal("refresh tx did not keep the balance service")
	}
	if cmd.dispatcher != dispatcher {
		t.Fatal("refresh tx did not keep the refresh dispatcher")
	}
}

func TestNew_Refresh_TxRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:tx: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshTx(RefreshTxDeps{Dispatcher: &refreshAddressDispatcherStub{}})
}

func TestNew_Refresh_TxRequiresADispatcher(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:tx: refresh dispatcher is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshTx(RefreshTxDeps{Balances: refresh.NewBalanceService(refresh.Deps{})})
}
