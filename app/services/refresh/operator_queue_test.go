package refresh

import (
	"errors"
	"testing"
)

func TestRefreshWalletQueueScopeDispatchesEveryJobForFull(t *testing.T) {
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

func TestRefreshWalletQueueScopeDispatchesOnlyTheNamedJob(t *testing.T) {
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

func TestRefreshWalletQueueScopeRejectsUnknownScope(t *testing.T) {
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

func TestRefreshWalletQueueScopeStopsOnTheFirstError(t *testing.T) {
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

func TestQueuedRefreshDispatchesWalletRefreshRequestedForBalances(t *testing.T) {
	recorder := &recordingDispatcher{}
	var events []recordedDispatch
	op := &Operator{
		dispatcher: recorder,
		requestRefresh: func(walletID, chainID string) error {
			events = append(events, recordedDispatch{kind: "event", walletID: walletID, chainID: chainID})
			return nil
		},
	}
	if err := op.dispatchQueuedRefresh("balances", "wallet-1", "eth"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(events) != 1 || events[0] != (recordedDispatch{kind: "event", walletID: "wallet-1", chainID: "eth"}) {
		t.Fatalf("events = %#v", events)
	}
	if len(recorder.calls) != 1 || recorder.calls[0] != (recordedDispatch{kind: "balances", walletID: "wallet-1", chainID: "eth"}) {
		t.Fatalf("calls = %#v", recorder.calls)
	}
}

func TestQueuedRefreshUsesTheEventForBalancesAndJobsForTheRest(t *testing.T) {
	recorder := &recordingDispatcher{}
	var events int
	op := &Operator{
		dispatcher: recorder,
		requestRefresh: func(walletID, chainID string) error {
			events++
			if walletID != "wallet-1" || chainID != "eth" {
				t.Fatalf("event payload = %s %s", walletID, chainID)
			}
			return nil
		},
	}
	if err := op.dispatchQueuedRefresh("full", "wallet-1", "eth"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if events != 1 {
		t.Fatalf("events = %d, want 1", events)
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

func TestQueuedRefreshLeavesANarrowScopeOnTheJobDispatcher(t *testing.T) {
	recorder := &recordingDispatcher{}
	op := &Operator{
		dispatcher: recorder,
		requestRefresh: func(string, string) error {
			t.Fatal("WalletRefreshRequested is the balances refresh")
			return nil
		},
	}
	if err := op.dispatchQueuedRefresh("transactions", "wallet-2", "btc"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(recorder.calls) != 1 || recorder.calls[0].kind != "transactions" {
		t.Fatalf("calls = %#v", recorder.calls)
	}
}

func TestQueuedRefreshStopsWhenTheEventDispatchFails(t *testing.T) {
	want := errors.New("event down")
	recorder := &recordingDispatcher{}
	op := &Operator{
		dispatcher: recorder,
		requestRefresh: func(string, string) error {
			return want
		},
	}
	err := op.dispatchQueuedRefresh("full", "wallet-1", "eth")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("jobs dispatched after the event failed: %#v", recorder.calls)
	}
}

func TestQueuedRefreshBalancesNeedsTheDispatcher(t *testing.T) {
	op := &Operator{requestRefresh: func(string, string) error { return nil }}
	err := op.dispatchQueuedRefresh("balances", "wallet-1", "eth")
	if err == nil || err.Error() != "refresh:wallet: refresh dispatcher is not initialized" {
		t.Fatalf("error = %v", err)
	}
}

func TestQueuedRefreshRejectsAMissingEventDispatcher(t *testing.T) {
	op := &Operator{}
	err := op.dispatchQueuedRefresh("balances", "wallet-1", "eth")
	if err == nil || err.Error() != "refresh:wallet: event dispatcher is not initialized" {
		t.Fatalf("error = %v", err)
	}
}

func TestRefreshWalletQueueScopeRejectsNilDispatcher(t *testing.T) {
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

func (r *recordingDispatcher) DispatchWalletCreated(string, string) error { return nil }

func (r *recordingDispatcher) DispatchWalletActivated(string, string) error { return nil }

func (r *recordingDispatcher) DispatchDepositDetected(string, string, string) error {
	return nil
}

func (r *recordingDispatcher) DispatchWithdrawalBroadcasted(string, string) error { return nil }

func (r *recordingDispatcher) record(kind, walletID, chainID string) error {
	r.calls = append(r.calls, recordedDispatch{kind: kind, walletID: walletID, chainID: chainID})
	if r.failKind == kind {
		return r.err
	}
	return nil
}
