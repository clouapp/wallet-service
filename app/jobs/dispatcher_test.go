package jobs

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/contracts/queue"
)

func TestDispatcher_Enqueues_TheSameBlockchainPayload(t *testing.T) {
	fake := &fakeQueue{}
	dispatcher := newDispatcher(func() Enqueuer { return fake }, nil)
	walletID := "11111111-1111-1111-1111-111111111111"
	chainID := "eth"

	cases := []struct {
		name      string
		dispatch  func(walletID, chainID string) error
		signature string
	}{
		{"balances", dispatcher.DispatchBalances, "refresh_wallet_balances"},
		{"transactions", dispatcher.DispatchTransactions, "refresh_wallet_transactions"},
		{"tokens", dispatcher.DispatchTokens, "refresh_wallet_tokens"},
		{"utxos", dispatcher.DispatchUTXOs, "refresh_wallet_utxos"},
		{"reconcile", dispatcher.DispatchReconcile, "reconcile_wallet_state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(fake.calls)
			if err := tc.dispatch(walletID, chainID); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if len(fake.calls) != before+1 {
				t.Fatalf("calls = %d, want %d", len(fake.calls), before+1)
			}
			got := fake.calls[len(fake.calls)-1]
			if got.signature != tc.signature {
				t.Fatalf("signature = %q, want %q", got.signature, tc.signature)
			}
			if got.connection != blockchainConnection {
				t.Fatalf("connection = %q, want %q", got.connection, blockchainConnection)
			}
			if got.queueName != blockchainQueueName {
				t.Fatalf("queue = %q, want %q", got.queueName, blockchainQueueName)
			}
			if len(got.args) != 1 || len(got.args[0]) != 1 {
				t.Fatalf("args = %#v, want one payload", got.args)
			}
			if got.args[0][0].Type != "string" {
				t.Fatalf("payload arg = %#v", got.args[0][0])
			}
			text, ok := got.args[0][0].Value.(string)
			if !ok {
				t.Fatalf("payload arg = %#v", got.args[0][0])
			}
			var payload walletDocument
			if err := json.Unmarshal([]byte(text), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.WalletID != walletID || payload.ChainID != chainID {
				t.Fatalf("payload = %#v", payload)
			}
		})
	}
}

func TestDispatcher_Returns_TheQueueError(t *testing.T) {
	want := errors.New("queue unavailable")
	fake := &fakeQueue{err: want}
	dispatcher := newDispatcher(func() Enqueuer { return fake }, nil)
	err := dispatcher.DispatchBalances("wallet", "btc")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestDispatcher_Fires_DomainEventsWithoutAQueueJob(t *testing.T) {
	queueFake := &fakeQueue{}
	events := &fakeEvents{}
	dispatcher := newDispatcher(func() Enqueuer { return queueFake }, func() EventBus { return events })
	walletID := "11111111-1111-1111-1111-111111111111"
	chainID := "eth"
	txHash := "0xabc"

	cases := []struct {
		name   string
		call   func() error
		event  string
		values []string
	}{
		{
			name:   "created",
			call:   func() error { return dispatcher.DispatchWalletCreated(walletID, chainID) },
			event:  "WalletCreated",
			values: []string{walletID, chainID},
		},
		{
			name:   "activated",
			call:   func() error { return dispatcher.DispatchWalletActivated(walletID, chainID) },
			event:  "WalletActivated",
			values: []string{walletID, chainID},
		},
		{
			name:   "withdrawal",
			call:   func() error { return dispatcher.DispatchWithdrawalBroadcasted(walletID, chainID) },
			event:  "WithdrawalBroadcasted",
			values: []string{walletID, chainID},
		},
		{
			name:   "deposit",
			call:   func() error { return dispatcher.DispatchDepositDetected(walletID, chainID, txHash) },
			event:  "DepositDetected",
			values: []string{walletID, chainID, txHash},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(events.calls)
			if err := tc.call(); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if len(queueFake.calls) != 0 {
				t.Fatalf("queue calls = %d, want 0", len(queueFake.calls))
			}
			if len(events.calls) != before+1 {
				t.Fatalf("event calls = %d, want %d", len(events.calls), before+1)
			}
			got := events.calls[len(events.calls)-1]
			if got.name != tc.event {
				t.Fatalf("event = %q, want %q", got.name, tc.event)
			}
			if len(got.args) != len(tc.values) {
				t.Fatalf("args = %#v, want %d", got.args, len(tc.values))
			}
			for i, value := range tc.values {
				if got.args[i].Type != "string" || got.args[i].Value != value {
					t.Fatalf("arg %d = %#v, want %q", i, got.args[i], value)
				}
			}
		})
	}
}

func TestDispatcher_Skips_DomainEventsWhenTheBusIsNil(t *testing.T) {
	dispatcher := newDispatcher(func() Enqueuer { return &fakeQueue{} }, nil)
	if err := dispatcher.DispatchWalletCreated("wallet", "eth"); err != nil {
		t.Fatal(err)
	}
	missing := newDispatcher(func() Enqueuer { return &fakeQueue{} }, func() EventBus { return nil })
	if err := missing.DispatchDepositDetected("wallet", "eth", "0xabc"); err != nil {
		t.Fatal(err)
	}
}

func TestNew_Dispatcher_RejectsNilClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for a nil queue client")
		}
	}()
	newDispatcher(nil, nil)
}

type recordedDispatch struct {
	signature  string
	args       [][]queue.Arg
	connection string
	queueName  string
}

type fakeQueue struct {
	calls []recordedDispatch
	err   error
}

func (f *fakeQueue) Job(job queue.Job, args ...[]queue.Arg) queue.PendingJob {
	return &fakePending{
		queue: f,
		call: recordedDispatch{
			signature: job.Signature(),
			args:      args,
		},
	}
}

type fakePending struct {
	queue *fakeQueue
	call  recordedDispatch
}

func (p *fakePending) Delay(time.Time) queue.PendingJob { return p }

func (p *fakePending) Dispatch() error {
	p.queue.calls = append(p.queue.calls, p.call)
	return p.queue.err
}

func (p *fakePending) DispatchSync() error { return p.queue.err }

func (p *fakePending) OnConnection(connection string) queue.PendingJob {
	p.call.connection = connection
	return p
}

func (p *fakePending) OnQueue(name string) queue.PendingJob {
	p.call.queueName = name
	return p
}

type recordedEvent struct {
	name string
	args []event.Arg
}

type fakeEvents struct {
	calls []recordedEvent
}

func (f *fakeEvents) Job(evt event.Event, args []event.Arg) event.Task {
	name := ""
	if evt != nil {
		eventType := reflect.TypeOf(evt)
		if eventType.Kind() == reflect.Pointer {
			eventType = eventType.Elem()
		}
		name = eventType.Name()
	}
	return &fakeEventTask{events: f, call: recordedEvent{name: name, args: args}}
}

type fakeEventTask struct {
	events *fakeEvents
	call   recordedEvent
}

func (t *fakeEventTask) Dispatch() error {
	t.events.calls = append(t.events.calls, t.call)
	return nil
}
