package xrp

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/xrpstream"
	"github.com/macrowallets/waas/pkg/types"
)

type watchedAddresses []string

func (w watchedAddresses) PluckActiveAddresses(context.Context, string) ([]string, error) {
	return append([]string(nil), w...), nil
}

// streamedRecord is one call the listener made to record deposits.
type streamedRecord struct {
	head      uint64
	transfers []types.DetectedTransfer
}

type recordingDeposits chan streamedRecord

func (r recordingDeposits) RecordValidatedTransfers(_ context.Context, _ string, head uint64, transfers []types.DetectedTransfer) (int, error) {
	r <- streamedRecord{head: head, transfers: transfers}
	return len(transfers), nil
}

func (r recordingDeposits) await(t *testing.T) streamedRecord {
	t.Helper()
	select {
	case got := <-r:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("nothing recorded")
		return streamedRecord{}
	}
}

// The listener over the real WebSocket session, against a fake rippled: a payment
// streamed on the first connection is recorded at once; the connection drops; the
// listener reconnects and reads, with account_tx, the ledgers from the last one it
// heard to the one its new subscription starts at.
func TestStream_ListenerRecordsStreamedDepositsAndBackfillsAfterAReconnect(t *testing.T) {
	fields, meta := recordedPayment(t)
	const missedHash = "C0FFEE00000000000000000000000000000000000000000000000000000000AA"
	missed := streamedV2(fields, meta)
	missedEntry := map[string]any{"meta": meta, "tx_json": missed["tx_json"], "hash": missedHash, "ledger_index": ledgerTaggedPayment + 3, "validated": true}
	var connections atomic.Int32
	f := newFakeXRPLSocket(t, func(request map[string]any) []any {
		switch request["command"] {
		case "subscribe":
			if request["streams"] != nil {
				ledger := ledgerTaggedPayment - 4
				if connections.Add(1) > 1 {
					ledger = ledgerTaggedPayment + 6
				}
				return []any{success(request, map[string]any{"ledger_index": ledger})}
			}
		case "account_tx":
			return []any{success(request, map[string]any{"transactions": []any{missedEntry}})}
		}
		return emptySuccess(request)
	})
	deposits := make(recordingDeposits, 4)
	listener := xrpstream.NewService(xrpstream.Deps{
		ChainID:   models.ChainTXRP,
		Dialer:    NewStreamDialer(StreamConfig{ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, URL: f.url(), IdleTimeout: 5 * time.Second}),
		Addresses: watchedAddresses{taggedRecipient}, Deposits: deposits,
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

	if accounts := f.awaitRequest(t, "subscribe"); accounts["streams"] == nil {
		t.Fatalf("first request %v, want the ledger stream", accounts)
	}
	if accounts := f.awaitRequest(t, "subscribe"); len(accounts["accounts"].([]any)) != 1 || accounts["accounts"].([]any)[0] != taggedRecipient {
		t.Fatalf("subscribe request %v, want the watched address", accounts)
	}
	f.push(streamedV1(fields, meta, true))
	streamed := deposits.await(t)
	if streamed.head != ledgerTaggedPayment || len(streamed.transfers) != 1 || streamed.transfers[0].TxHash != taggedPaymentHash ||
		streamed.transfers[0].DestinationTag == nil || *streamed.transfers[0].DestinationTag != 777001 {
		t.Fatalf("streamed record %+v", streamed)
	}
	f.push(map[string]any{"type": "ledgerClosed", "ledger_index": ledgerTaggedPayment + 1})
	f.push(map[string]any{"type": "ledgerClosed", "ledger_index": ledgerTaggedPayment + 2})

	f.drop()

	f.awaitRequest(t, "subscribe")
	f.awaitRequest(t, "subscribe")
	backfill := f.awaitRequest(t, "account_tx")
	if backfill["account"] != taggedRecipient || backfill["ledger_index_min"] != float64(ledgerTaggedPayment+2) || backfill["ledger_index_max"] != float64(ledgerTaggedPayment+6) {
		t.Fatalf("account_tx %v, want the ledgers from the last one heard (%d) to the new subscription's (%d)", backfill, ledgerTaggedPayment+2, ledgerTaggedPayment+6)
	}
	recovered := deposits.await(t)
	if recovered.head != ledgerTaggedPayment+6 || len(recovered.transfers) != 1 || !strings.EqualFold(recovered.transfers[0].TxHash, missedHash) ||
		recovered.transfers[0].BlockNumber != ledgerTaggedPayment+3 {
		t.Fatalf("backfilled record %+v", recovered)
	}
}
