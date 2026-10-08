package jobs

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/queue"
)

func TestDispatcher_Enqueues_TheSameBlockchainPayload(t *testing.T) {
	fake := &fakeQueue{}
	dispatcher := newDispatcher(func() Enqueuer { return fake })
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
	dispatcher := newDispatcher(func() Enqueuer { return fake })
	err := dispatcher.DispatchBalances("wallet", "btc")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestNew_Dispatcher_RejectsNilClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for a nil queue client")
		}
	}()
	newDispatcher(nil)
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
