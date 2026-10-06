package commands

import (
	"testing"

	"github.com/macrowallets/waas/app/services/refresh"
)

type refreshAddressDispatcherStub struct{}

func (refreshAddressDispatcherStub) DispatchBalances(string, string) error     { return nil }
func (refreshAddressDispatcherStub) DispatchTransactions(string, string) error { return nil }
func (refreshAddressDispatcherStub) DispatchTokens(string, string) error       { return nil }
func (refreshAddressDispatcherStub) DispatchUTXOs(string, string) error        { return nil }
func (refreshAddressDispatcherStub) DispatchReconcile(string, string) error    { return nil }
func (refreshAddressDispatcherStub) DispatchWalletCreated(string, string) error {
	return nil
}
func (refreshAddressDispatcherStub) DispatchWalletActivated(string, string) error {
	return nil
}
func (refreshAddressDispatcherStub) DispatchDepositDetected(string, string, string) error {
	return nil
}
func (refreshAddressDispatcherStub) DispatchWithdrawalBroadcasted(string, string) error {
	return nil
}

func TestNewRefreshAddressKeepsItsDependencies(t *testing.T) {
	balances := refresh.NewBalanceService(refresh.Deps{})
	dispatcher := &refreshAddressDispatcherStub{}
	cmd := NewRefreshAddress(RefreshAddressDeps{
		Balances:   balances,
		Dispatcher: dispatcher,
	})
	if cmd == nil {
		t.Fatal("NewRefreshAddress returned nil")
	}
	if cmd.balances != balances {
		t.Fatal("refresh address did not keep the balance service")
	}
	if cmd.dispatcher != dispatcher {
		t.Fatal("refresh address did not keep the refresh dispatcher")
	}
}

func TestNewRefreshAddressRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:address: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshAddress(RefreshAddressDeps{Dispatcher: &refreshAddressDispatcherStub{}})
}

func TestNewRefreshAddressRequiresADispatcher(t *testing.T) {
	defer func() {
		got := recover()
		if got != "refresh:address: refresh dispatcher is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewRefreshAddress(RefreshAddressDeps{Balances: refresh.NewBalanceService(refresh.Deps{})})
}
