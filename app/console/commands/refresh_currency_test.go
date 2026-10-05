package commands

import (
	"testing"

	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/refresh"
)

func TestNewRefreshCurrencyKeepsItsDependencies(t *testing.T) {
	registry := chainpkg.NewRegistry()
	balances := refresh.NewBalanceService(refresh.Deps{})
	dispatcher := &refreshAddressDispatcherStub{}
	cmd := NewRefreshCurrency(RefreshCurrencyDeps{
		Registry:   registry,
		Balances:   balances,
		Dispatcher: dispatcher,
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
	if cmd.dispatcher != dispatcher {
		t.Fatal("refresh currency did not keep the refresh dispatcher")
	}
}

func TestNewRefreshCurrencyRequiresARegistry(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:currency: chain registry is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshCurrency(RefreshCurrencyDeps{
		Balances:   refresh.NewBalanceService(refresh.Deps{}),
		Dispatcher: &refreshAddressDispatcherStub{},
	})
}

func TestNewRefreshCurrencyRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:currency: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshCurrency(RefreshCurrencyDeps{
		Registry:   chainpkg.NewRegistry(),
		Dispatcher: &refreshAddressDispatcherStub{},
	})
}

func TestNewRefreshCurrencyRequiresADispatcher(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:currency: refresh dispatcher is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshCurrency(RefreshCurrencyDeps{
		Registry: chainpkg.NewRegistry(),
		Balances: refresh.NewBalanceService(refresh.Deps{}),
	})
}
