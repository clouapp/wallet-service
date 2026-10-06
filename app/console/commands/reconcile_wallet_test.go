package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/security"
)

type reconcileWalletDispatcherStub struct{}

func (reconcileWalletDispatcherStub) DispatchBalances(string, string) error     { return nil }
func (reconcileWalletDispatcherStub) DispatchTransactions(string, string) error { return nil }
func (reconcileWalletDispatcherStub) DispatchTokens(string, string) error       { return nil }
func (reconcileWalletDispatcherStub) DispatchUTXOs(string, string) error        { return nil }
func (reconcileWalletDispatcherStub) DispatchReconcile(string, string) error    { return nil }
func (reconcileWalletDispatcherStub) DispatchWalletCreated(string, string) error {
	return nil
}
func (reconcileWalletDispatcherStub) DispatchWalletActivated(string, string) error {
	return nil
}
func (reconcileWalletDispatcherStub) DispatchDepositDetected(string, string, string) error {
	return nil
}
func (reconcileWalletDispatcherStub) DispatchWithdrawalBroadcasted(string, string) error {
	return nil
}

func TestNewReconcileWalletKeepsItsDependencies(t *testing.T) {
	balances := refresh.NewBalanceService(refresh.Deps{})
	dispatcher := &reconcileWalletDispatcherStub{}
	cmd := NewReconcileWallet(ReconcileWalletDeps{
		Balances:   balances,
		Dispatcher: dispatcher,
	})
	if cmd == nil {
		t.Fatal("NewReconcileWallet returned nil")
	}
	if cmd.balances != balances {
		t.Fatal("reconcile wallet did not keep the balance service")
	}
	if cmd.dispatcher != dispatcher {
		t.Fatal("reconcile wallet did not keep the refresh dispatcher")
	}
}

func TestNewReconcileWalletRequiresBalances(t *testing.T) {
	defer func() {
		got := recover()
		if got != "reconcile:wallet: balance refresh service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewReconcileWallet(ReconcileWalletDeps{Dispatcher: &reconcileWalletDispatcherStub{}})
}

func TestNewReconcileWalletRequiresADispatcher(t *testing.T) {
	defer func() {
		got := recover()
		if got != "reconcile:wallet: refresh dispatcher is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewReconcileWallet(ReconcileWalletDeps{Balances: refresh.NewBalanceService(refresh.Deps{})})
}

func TestReconciliationFailureLineOmitsRPCCredential(t *testing.T) {
	const fixture = "fixture-rpc-query-key"
	security.ConfigureRedaction([]string{"btc.example"}, nil)
	t.Cleanup(func() { security.ConfigureRedaction(nil, nil) })

	err := errors.New(`get native balance: Get "https://user:` + fixture + `@btc.example/v2/` + fixture + `?apikey=` + fixture + `": dial tcp`)
	line := redactedLine("reconciliation failed: " + err.Error())
	if strings.Contains(line, fixture) {
		t.Fatal("reconciliation failure line wrote the RPC credential")
	}
}
