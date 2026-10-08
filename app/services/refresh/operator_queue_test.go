package refresh

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestRefresh_Wallet_QueueScopeDispatchesEveryJobForFull(t *testing.T) {
	recorder := &recordingDispatcher{}
	op := &Operator{dispatcher: recorder}

	if err := op.dispatchScopedJobs("full", "wallet-1", "eth"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := []recordedDispatch{
		{kind: "balances", walletID: "wallet-1", chainID: "eth"},
		{kind: "transactions", walletID: "wallet-1", chainID: "eth"},
		{kind: "tokens", walletID: "wallet-1", chainID: "eth"},
		{kind: "utxos", walletID: "wallet-1", chainID: "eth"},
	}
	if len(recorder.calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", recorder.calls, want)
	}
	for i, call := range want {
		if recorder.calls[i] != call {
			t.Fatalf("call %d = %#v, want %#v", i, recorder.calls[i], call)
		}
	}
}

func TestRefresh_Wallet_QueueScopeDispatchesOnlyTheNamedJob(t *testing.T) {
	for _, scope := range []string{"balances", "transactions", "tokens", "utxos"} {
		t.Run(scope, func(t *testing.T) {
			recorder := &recordingDispatcher{}
			op := &Operator{dispatcher: recorder}
			if err := op.dispatchScopedJobs(scope, "wallet-2", "btc"); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if len(recorder.calls) != 1 || recorder.calls[0].kind != scope {
				t.Fatalf("calls = %#v, want one %s", recorder.calls, scope)
			}
			if recorder.calls[0].walletID != "wallet-2" || recorder.calls[0].chainID != "btc" {
				t.Fatalf("payload = %#v", recorder.calls[0])
			}
		})
	}
}

func TestRefresh_Wallet_QueueScopeRejectsUnknownScope(t *testing.T) {
	recorder := &recordingDispatcher{}
	op := &Operator{dispatcher: recorder}
	err := op.dispatchScopedJobs("nope", "wallet-1", "eth")
	if err == nil || err.Error() != "unknown scope: nope" {
		t.Fatalf("error = %v", err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("unknown scope dispatched %#v", recorder.calls)
	}
}

func TestRefresh_Wallet_QueueScopeStopsOnTheFirstError(t *testing.T) {
	want := errors.New("queue down")
	recorder := &recordingDispatcher{failKind: "transactions", err: want}
	op := &Operator{dispatcher: recorder}
	err := op.dispatchScopedJobs("full", "wallet-1", "eth")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("calls = %#v, want balances then transactions", recorder.calls)
	}
}

func TestRefresh_Wallet_QueueFlagEnqueuesTheJobs(t *testing.T) {
	id := uuid.New()
	recorder := &recordingDispatcher{}
	op := NewOperator(OperatorDeps{
		Dispatcher: recorder,
		Wallets:    walletLookupStub{wallet: &models.Wallet{ID: id, Chain: "eth"}},
	})
	out, err := op.RefreshWallet(context.Background(), WalletCommand{
		WalletID: id.String(), Scope: "balances", Queue: true, Reason: "manual",
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(out.Info) != 1 {
		t.Fatalf("info = %#v", out.Info)
	}
	want := recordedDispatch{kind: "balances", walletID: id.String(), chainID: "eth"}
	if len(recorder.calls) != 1 || recorder.calls[0] != want {
		t.Fatalf("calls = %#v, want %#v", recorder.calls, want)
	}
}

type walletLookupStub struct{ wallet *models.Wallet }

func (s walletLookupStub) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return s.wallet, nil
}

func TestRefresh_Wallet_QueueScopeRejectsNilDispatcher(t *testing.T) {
	op := &Operator{}
	err := op.dispatchScopedJobs("balances", "wallet-1", "eth")
	if err == nil || err.Error() != "refresh:wallet: refresh dispatcher is not initialized" {
		t.Fatalf("error = %v", err)
	}
}

type recordedDispatch struct {
	kind     string
	walletID string
	chainID  string
}

type recordingDispatcher struct {
	calls    []recordedDispatch
	failKind string
	err      error
}

func (r *recordingDispatcher) DispatchBalances(walletID, chainID string) error {
	return r.record("balances", walletID, chainID)
}

func (r *recordingDispatcher) DispatchTransactions(walletID, chainID string) error {
	return r.record("transactions", walletID, chainID)
}

func (r *recordingDispatcher) DispatchTokens(walletID, chainID string) error {
	return r.record("tokens", walletID, chainID)
}

func (r *recordingDispatcher) DispatchUTXOs(walletID, chainID string) error {
	return r.record("utxos", walletID, chainID)
}

func (r *recordingDispatcher) DispatchReconcile(walletID, chainID string) error {
	return r.record("reconcile", walletID, chainID)
}

func (r *recordingDispatcher) record(kind, walletID, chainID string) error {
	r.calls = append(r.calls, recordedDispatch{kind: kind, walletID: walletID, chainID: chainID})
	if r.failKind == kind {
		return r.err
	}
	return nil
}
