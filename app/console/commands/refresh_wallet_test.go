package commands

import (
	"testing"

	"github.com/macrowallets/waas/app/services/refresh"
)

func TestNewRefreshWalletKeepsItsDependencies(t *testing.T) {
	balances := refresh.NewBalanceService(refresh.Deps{})
	dispatcher := &refreshAddressDispatcherStub{}
	cmd := NewRefreshWallet(RefreshWalletDeps{
		Balances:   balances,
		Dispatcher: dispatcher,
	})
	if cmd == nil {
		t.Fatal("NewRefreshWallet returned nil")
	}
	if cmd.balances != balances {
		t.Fatal("refresh wallet did not keep the balance service")
	}
	if cmd.dispatcher != dispatcher {
		t.Fatal("refresh wallet did not keep the refresh dispatcher")
	}
}

func TestNewRefreshWalletRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:wallet: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshWallet(RefreshWalletDeps{Dispatcher: &refreshAddressDispatcherStub{}})
}

func TestNewRefreshWalletRequiresADispatcher(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:wallet: refresh dispatcher is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshWallet(RefreshWalletDeps{Balances: refresh.NewBalanceService(refresh.Deps{})})
}
