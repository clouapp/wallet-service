package xrp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/xrpstream"
)

const (
	taggedLedgerHash = "28D45A55AB2C0C4EC4F4B6F305D75DE861FD4F675301EC1035622910CD14FB2D"
	taggedRecipient  = "rai1iQmERbDPvfuk31XVsYs6ZXeA8QRZnC"
	taggedSender     = "rDWd81gTyMMA1arNv9bH1qcBDN4EaozN1Y"
)

// fakeXRPLSocket is a rippled WebSocket stand-in: it answers each request with
// what answer returns (stream messages first, then the response) and can push
// stream messages at any time.
type fakeXRPLSocket struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	conn     *websocket.Conn
	requests []map[string]any
	answer   func(request map[string]any) []any
	accepted chan struct{}
	// seen gets every request as it arrives.
	seen chan map[string]any
}

func newFakeXRPLSocket(t *testing.T, answer func(request map[string]any) []any) *fakeXRPLSocket {
	t.Helper()
	f := &fakeXRPLSocket{t: t, answer: answer, accepted: make(chan struct{}, 8), seen: make(chan map[string]any, 256)}
	upgrader := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conn = conn
		f.mu.Unlock()
		f.accepted <- struct{}{}
		for {
			var request map[string]any
			if err := conn.ReadJSON(&request); err != nil {
				return
			}
			f.mu.Lock()
			f.requests = append(f.requests, request)
			f.mu.Unlock()
			select {
			case f.seen <- request:
			default:
			}
			for _, message := range f.answer(request) {
				f.push(message)
			}
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeXRPLSocket) url() string { return "ws" + strings.TrimPrefix(f.srv.URL, "http") }

func (f *fakeXRPLSocket) push(message any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn == nil {
		f.t.Error("push before a client connected")
		return
	}
	if err := f.conn.WriteJSON(message); err != nil {
		f.t.Logf("push: %v", err)
	}
}

// drop closes the client's connection, as a server restart or a network cut does.
func (f *fakeXRPLSocket) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		_ = f.conn.Close()
	}
}

// awaitRequest returns the next request whose command is command.
func (f *fakeXRPLSocket) awaitRequest(t *testing.T, command string) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case request := <-f.seen:
			if request["command"] == command {
				return request
			}
		case <-deadline:
			t.Fatalf("no %s request", command)
			return nil
		}
	}
}

func (f *fakeXRPLSocket) recorded() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.requests...)
}

func success(request map[string]any, result any) map[string]any {
	return map[string]any{"id": request["id"], "type": "response", "status": "success", "result": result}
}

func emptySuccess(request map[string]any) []any {
	return []any{success(request, map[string]any{})}
}

func dialFake(t *testing.T, f *fakeXRPLSocket, idle time.Duration) xrpstream.Session {
	t.Helper()
	session, err := NewStreamDialer(StreamConfig{ChainIDStr: models.ChainTXRP, NativeSymbol: models.NativeXRP, URL: f.url(), IdleTimeout: idle}).Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	<-f.accepted
	return session
}

// recordedPayment is the tagged altnet Payment of ledger 21451514 split into its
// fields (hash included) and its metadata, as the API v1 stream and account_tx
// carry them.
func recordedPayment(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	var answer struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(readLedgerFixture(t, ledgerFixtures[ledgerTaggedPayment]), &answer); err != nil {
		t.Fatal(err)
	}
	tx := recordedTransaction(t, answer.Result, taggedPaymentHash)
	fields := map[string]any{}
	for key, value := range tx {
		if key != "metaData" {
			fields[key] = value
		}
	}
	return fields, tx["metaData"].(map[string]any)
}

// streamedV1 is the transaction message of the API v1 stream for fields and meta.
func streamedV1(fields, meta map[string]any, validated bool) map[string]any {
	return map[string]any{
		"type": "transaction", "validated": validated, "engine_result": meta["TransactionResult"],
		"ledger_index": ledgerTaggedPayment, "ledger_hash": taggedLedgerHash, "meta": meta, "transaction": fields,
	}
}

// streamedV2 is the same message under API v2: tx_json, with the hash outside it.
func streamedV2(fields, meta map[string]any) map[string]any {
	txJSON := map[string]any{}
	for key, value := range fields {
		if key != "hash" {
			txJSON[key] = value
		}
	}
	return map[string]any{
		"type": "transaction", "validated": true, "engine_result": meta["TransactionResult"], "hash": fields["hash"],
		"ledger_index": ledgerTaggedPayment, "ledger_hash": taggedLedgerHash, "meta": meta, "tx_json": txJSON,
	}
}

func clone(m map[string]any) map[string]any {
	raw, _ := json.Marshal(m)
	var copied map[string]any
	_ = json.Unmarshal(raw, &copied)
	return copied
}

func TestStream_SubscribeLedgersReturnsTheValidatedLedger(t *testing.T) {
	f := newFakeXRPLSocket(t, func(request map[string]any) []any {
		return []any{success(request, map[string]any{"ledger_index": 21451514, "ledger_hash": taggedLedgerHash, "validated_ledgers": "1-21451514"})}
	})
	session := dialFake(t, f, time.Second)

	ledger, err := session.SubscribeLedgers(context.Background())

	if err != nil || ledger != 21451514 {
		t.Fatalf("ledger %d err %v", ledger, err)
	}
	request := f.recorded()[0]
	if request["command"] != "subscribe" || fmt.Sprint(request["streams"]) != "[ledger]" || request["id"] == nil {
		t.Fatalf("request %v", request)
	}
}

func TestStream_NextReadsAValidatedPaymentInBothAPIVersions(t *testing.T) {
	f := newFakeXRPLSocket(t, emptySuccess)
	session := dialFake(t, f, time.Second)
	fields, meta := recordedPayment(t)

	for name, message := range map[string]map[string]any{"api v1": streamedV1(fields, meta, true), "api v2": streamedV2(fields, meta)} {
		f.push(message)
		event, err := session.Next(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if event.Ledger != ledgerTaggedPayment || len(event.Deposits) != 1 {
			t.Fatalf("%s: event %+v", name, event)
		}
		got := event.Deposits[0]
		if got.TxHash != taggedPaymentHash || got.From != taggedSender || got.To != taggedRecipient || got.Amount.String() != "1000000" ||
			got.Asset != models.NativeXRP || got.BlockNumber != ledgerTaggedPayment || got.BlockHash != taggedLedgerHash || got.LogIndex != 7 ||
			got.DestinationTag == nil || *got.DestinationTag != 777001 {
			t.Fatalf("%s: deposit %+v", name, got)
		}
	}
}

func TestStream_NextReportsNoDepositForWhatIsNotOne(t *testing.T) {
	f := newFakeXRPLSocket(t, emptySuccess)
	session := dialFake(t, f, time.Second)
	fields, meta := recordedPayment(t)
	failed := clone(meta)
	failed["TransactionResult"] = "tecUNFUNDED_PAYMENT"
	issued := clone(meta)
	issued["delivered_amount"] = map[string]any{"currency": "USD", "issuer": taggedSender, "value": "5"}
	toItself := clone(fields)
	toItself["Destination"] = taggedSender
	offer := clone(fields)
	offer["TransactionType"] = "OfferCreate"

	cases := map[string]map[string]any{
		"not validated":    streamedV1(fields, meta, false),
		"failed payment":   streamedV1(fields, failed, true),
		"issued currency":  streamedV1(fields, issued, true),
		"payment to self":  streamedV1(toItself, meta, true),
		"other type":       streamedV1(offer, meta, true),
		"validated ledger": {"type": "ledgerClosed", "ledger_index": 21451515, "ledger_hash": taggedLedgerHash},
	}
	for name, message := range cases {
		f.push(message)
		event, err := session.Next(context.Background())
		if err != nil || len(event.Deposits) != 0 {
			t.Fatalf("%s: event %+v err %v, want no deposit", name, event, err)
		}
		if name == "validated ledger" && event.Ledger != 21451515 {
			t.Fatalf("ledgerClosed: event %+v, want ledger 21451515", event)
		}
		if name == "not validated" && event.Ledger != 0 {
			t.Fatalf("a transaction not validated names no validated ledger, got %d", event.Ledger)
		}
	}
}

func TestStream_AStreamMessageThatArrivesBeforeAnAnswerIsKept(t *testing.T) {
	fields, meta := recordedPayment(t)
	f := newFakeXRPLSocket(t, func(request map[string]any) []any {
		return []any{streamedV1(fields, meta, true), success(request, map[string]any{})}
	})
	session := dialFake(t, f, time.Second)

	if err := session.SubscribeAccounts(context.Background(), []string{taggedRecipient}); err != nil {
		t.Fatal(err)
	}

	event, err := session.Next(context.Background())
	if err != nil || len(event.Deposits) != 1 || event.Deposits[0].TxHash != taggedPaymentHash {
		t.Fatalf("event %+v err %v, want the payment streamed before the answer", event, err)
	}
}

func TestStream_SubscribeAccountsSendsTheAccountsInBatches(t *testing.T) {
	f := newFakeXRPLSocket(t, emptySuccess)
	session := dialFake(t, f, time.Second)
	accounts := make([]string, 1201)
	for i := range accounts {
		accounts[i] = fmt.Sprintf("r%033d", i)
	}

	if err := session.SubscribeAccounts(context.Background(), accounts); err != nil {
		t.Fatal(err)
	}
	if err := session.UnsubscribeAccounts(context.Background(), accounts[:2]); err != nil {
		t.Fatal(err)
	}

	requests := f.recorded()
	if len(requests) != 4 {
		t.Fatalf("%d requests, want three subscribe batches and one unsubscribe", len(requests))
	}
	for i, want := range []int{500, 500, 201} {
		if requests[i]["command"] != "subscribe" || len(requests[i]["accounts"].([]any)) != want || requests[i]["streams"] != nil {
			t.Fatalf("request %d: %s with %d accounts, want subscribe with %d", i, requests[i]["command"], len(requests[i]["accounts"].([]any)), want)
		}
	}
	if requests[3]["command"] != "unsubscribe" || len(requests[3]["accounts"].([]any)) != 2 {
		t.Fatalf("unsubscribe request %v", requests[3])
	}
}

func TestStream_AccountDepositsPagesThroughAccountTx(t *testing.T) {
	fields, meta := recordedPayment(t)
	v1Entry := map[string]any{"meta": meta, "tx": clone(fields), "validated": true}
	v1Entry["tx"].(map[string]any)["ledger_index"] = ledgerTaggedPayment
	outgoing := clone(fields)
	outgoing["Account"], outgoing["Destination"], outgoing["hash"] = taggedRecipient, taggedSender, strings.Repeat("A", 64)
	outgoing["ledger_index"] = ledgerTaggedPayment + 1
	v2 := streamedV2(fields, meta)
	v2Entry := map[string]any{"meta": meta, "tx_json": v2["tx_json"], "hash": strings.Repeat("B", 64), "ledger_index": ledgerTaggedPayment + 2, "validated": true}

	f := newFakeXRPLSocket(t, func(request map[string]any) []any {
		if request["marker"] == nil {
			return []any{success(request, map[string]any{
				"transactions": []any{v1Entry, map[string]any{"meta": meta, "tx": outgoing, "validated": true}},
				"marker":       map[string]any{"ledger": 21451515, "seq": 3},
			})}
		}
		return []any{success(request, map[string]any{"transactions": []any{v2Entry}})}
	})
	session := dialFake(t, f, time.Second)

	deposits, err := session.AccountDeposits(context.Background(), taggedRecipient, 21451500, 21451600)

	if err != nil {
		t.Fatal(err)
	}
	if len(deposits) != 2 || deposits[0].TxHash != taggedPaymentHash || deposits[0].BlockNumber != ledgerTaggedPayment ||
		deposits[1].TxHash != strings.Repeat("B", 64) || deposits[1].BlockNumber != ledgerTaggedPayment+2 {
		t.Fatalf("deposits %+v, want the two payments to the account and not the one it sent", deposits)
	}
	requests := f.recorded()
	if len(requests) != 2 {
		t.Fatalf("%d requests, want two pages", len(requests))
	}
	first := requests[0]
	if first["command"] != "account_tx" || first["account"] != taggedRecipient || first["ledger_index_min"] != float64(21451500) ||
		first["ledger_index_max"] != float64(21451600) || first["forward"] != true || first["limit"] == nil {
		t.Fatalf("first page request %v", first)
	}
	if marker, ok := requests[1]["marker"].(map[string]any); !ok || marker["seq"] != float64(3) {
		t.Fatalf("second page request %v, want the marker of the first page", requests[1])
	}
}

func TestStream_AccountDepositsOfAnAccountNotFundedYetAreNone(t *testing.T) {
	f := newFakeXRPLSocket(t, func(request map[string]any) []any {
		return []any{map[string]any{"id": request["id"], "type": "response", "status": "error", "error": "actNotFound"}}
	})
	session := dialFake(t, f, time.Second)

	deposits, err := session.AccountDeposits(context.Background(), taggedRecipient, 1, 2)

	if err != nil || len(deposits) != 0 {
		t.Fatalf("deposits %+v err %v", deposits, err)
	}
}

func TestStream_AnErrorAnswerIsAnError(t *testing.T) {
	f := newFakeXRPLSocket(t, func(request map[string]any) []any {
		return []any{map[string]any{"id": request["id"], "type": "response", "status": "error", "error": "noPermission"}}
	})
	session := dialFake(t, f, time.Second)

	if _, err := session.SubscribeLedgers(context.Background()); err == nil || !strings.Contains(err.Error(), "noPermission") {
		t.Fatalf("err %v, want the server's error code", err)
	}
}

func TestStream_ASilentEndpointFailsNext(t *testing.T) {
	f := newFakeXRPLSocket(t, emptySuccess)
	session := dialFake(t, f, 50*time.Millisecond)

	started := time.Now()
	_, err := session.Next(context.Background())

	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("err %v after %s, want the idle timeout", err, time.Since(started))
	}
}

func TestStream_CancellingTheContextEndsNext(t *testing.T) {
	f := newFakeXRPLSocket(t, emptySuccess)
	session := dialFake(t, f, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	_, err := session.Next(ctx)

	if err == nil || ctx.Err() == nil {
		t.Fatalf("err %v, want the cancellation", err)
	}
}

func TestStream_DialRefusesAnotherSchemeAndNeverReportsTheURL(t *testing.T) {
	if _, err := NewStreamDialer(StreamConfig{URL: "https://s.altnet.rippletest.net:51234"}).Dial(context.Background()); err == nil {
		t.Fatal("an http URL was dialed as a WebSocket")
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	keyed := "ws" + strings.TrimPrefix(closed.URL, "http") + "/key-0123456789abcdef"
	closed.Close()

	_, err := NewStreamDialer(StreamConfig{URL: keyed}).Dial(context.Background())

	if err == nil || strings.Contains(err.Error(), "key-0123456789abcdef") {
		t.Fatalf("err %v, want a dial error without the URL", err)
	}
}
