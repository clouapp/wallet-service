package xrp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/xrpstream"
)

type watchedAddresses []string

func (w watchedAddresses) PluckActiveAddresses(context.Context, string) ([]string, error) {
	return append([]string(nil), w...), nil
}

// readLedgers records the ledgers the listener asks the deposit service to read.
type readLedgers chan uint64

func (r readLedgers) RecordStreamedLedger(_ context.Context, _ string, ledger uint64) (int, error) {
	r <- ledger
	return 1, nil
}

func (r readLedgers) await(t *testing.T, want ...uint64) {
	t.Helper()
	for _, ledger := range want {
		select {
		case got := <-r:
			if got != ledger {
				t.Fatalf("read ledger %d, want %d", got, ledger)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no ledger read, want %d", ledger)
		}
	}
}

// The listener over the real WebSocket session, against a fake rippled: the
// network is checked on every dial, the addresses are subscribed before the
// ledger stream, a payment streamed to a watched address has its ledger read
// through the chain RPC at once, and after a dropped connection the listener reads
// the ledgers from the last one it heard to the one its new subscription starts at.
func TestStream_ListenerReadsTheLedgersOfStreamedDepositsAndOfAReconnectGap(t *testing.T) {
	fields, meta := recordedPayment(t)
	var connections atomic.Int32
	f := newFakeXRPLSocket(t, func(request map[string]any) []any {
		if request["command"] == "subscribe" && request["streams"] != nil {
			ledger := ledgerTaggedPayment - 4
			if connections.Add(1) > 1 {
				ledger = ledgerTaggedPayment + 4
			}
			return []any{success(request, map[string]any{"ledger_index": ledger})}
		}
		return emptySuccess(request)
	})
	ledgers := make(readLedgers, 16)
	listener := xrpstream.NewService(xrpstream.Deps{
		ChainID:   models.ChainTXRP,
		Dialer:    newTestStreamDialer(StreamConfig{ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, URL: f.url(), IsTestnet: true, IdleTimeout: 5 * time.Second}),
		Addresses: watchedAddresses{taggedRecipient}, Deposits: ledgers,
		AddressRefresh: time.Hour, ReconnectMin: time.Millisecond, ReconnectMax: time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		listener.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	f.awaitRequest(t, "server_info")
	if accounts := f.awaitRequest(t, "subscribe"); accounts["streams"] != nil || len(accounts["accounts"].([]any)) != 1 || accounts["accounts"].([]any)[0] != taggedRecipient {
		t.Fatalf("first subscription %v, want the watched address before the ledger stream", accounts)
	}
	if stream := f.awaitRequest(t, "subscribe"); stream["streams"] == nil {
		t.Fatalf("second subscription %v, want the ledger stream", stream)
	}
	f.push(streamedV1(fields, meta, true))
	ledgers.await(t, ledgerTaggedPayment)
	f.push(map[string]any{"type": "ledgerClosed", "ledger_index": ledgerTaggedPayment + 1})
	f.push(map[string]any{"type": "ledgerClosed", "ledger_index": ledgerTaggedPayment + 2})

	f.drop()

	f.awaitRequest(t, "server_info")
	f.awaitRequest(t, "subscribe")
	f.awaitRequest(t, "subscribe")
	ledgers.await(t, ledgerTaggedPayment+2, ledgerTaggedPayment+3, ledgerTaggedPayment+4)
}
