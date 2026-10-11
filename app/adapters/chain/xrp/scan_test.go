package xrp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// Altnet ledgers recorded with the rippled ledger method (transactions and expand
// true) into testdata/xrpl, answered as they were received.
const (
	// One native Payment carrying destination tag 777001, next to offers.
	ledgerTaggedPayment = uint64(21451514)
	// Five native Payments, one tecUNFUNDED_PAYMENT Payment and two CheckCash.
	ledgerFailedPayment = uint64(21451387)
	// One native Payment and one USD issued-currency Payment (tag 1005).
	ledgerIssuedCurrency = uint64(21451526)
	// Batch inner Payments an account makes to itself: partial cross-currency
	// conversions, two of which deliver XRP.
	ledgerSelfPayments = uint64(21451300)
	// A validated ledger without transactions.
	ledgerEmpty = uint64(21451524)
	// The open ledger right after the validated one: closed false, validated false.
	ledgerOpen = uint64(21451661)
	// A ledger the server did not have yet: lgrNotFound.
	ledgerNotFound = uint64(21451662)

	taggedPaymentHash = "429100F561631FEA1B5EC4BB26B0607E753F0A5157B38824206CD298318C7516"
)

var ledgerFixtures = map[uint64]string{
	ledgerTaggedPayment:  "ledger_21451514.json",
	ledgerFailedPayment:  "ledger_21451387.json",
	ledgerIssuedCurrency: "ledger_21451526.json",
	ledgerSelfPayments:   "ledger_21451300.json",
	ledgerEmpty:          "ledger_21451524.json",
	ledgerOpen:           "ledger_21451661_open.json",
	ledgerNotFound:       "ledger_21451662_not_found.json",
}

// ledgerRecordings holds the recorded answers; a fixture is read by name inside it.
var ledgerRecordings = os.DirFS(filepath.Join("testdata", "xrpl"))

func readLedgerFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := fs.ReadFile(ledgerRecordings, name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// recordedLedgerNode answers the ledger method from recorded responses, keyed by
// the ledger_index the request names, and keeps the requests it received.
type recordedLedgerNode struct {
	t       *testing.T
	mu      sync.Mutex
	ledgers map[uint64][]byte
	status  int
	params  []map[string]any
}

func newRecordedLedgerNode(t *testing.T) (*recordedLedgerNode, *Live) {
	t.Helper()
	node := &recordedLedgerNode{t: t, ledgers: map[uint64][]byte{}}
	for index, name := range ledgerFixtures {
		node.ledgers[index] = readLedgerFixture(t, name)
	}
	server := httptest.NewServer(http.HandlerFunc(node.serve))
	t.Cleanup(server.Close)
	adapter := NewLive(Config{
		ChainIDStr: models.ChainTXRP, ChainName: "XRP Ledger Testnet", NativeSymbol: models.NativeXRP,
		RPCURL: server.URL, IsTestnet: true, Confirmations: 1,
	})
	return node, adapter
}

func (n *recordedLedgerNode) serve(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		n.t.Error(err)
		return
	}
	var req struct {
		Method string           `json:"method"`
		Params []map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil || req.Method != "ledger" || len(req.Params) != 1 {
		n.t.Errorf("unexpected request %s", raw)
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.params = append(n.params, req.Params[0])
	if n.status != 0 {
		http.Error(w, "unavailable", n.status)
		return
	}
	index, _ := req.Params[0]["ledger_index"].(float64)
	body, ok := n.ledgers[uint64(index)]
	if !ok {
		n.t.Errorf("no recorded ledger %v", req.Params[0]["ledger_index"])
		http.Error(w, "no recording", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// requests returns the params of every ledger request received.
func (n *recordedLedgerNode) requests() []map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]map[string]any(nil), n.params...)
}

// answer makes the node answer body for ledger index.
func (n *recordedLedgerNode) answer(index uint64, body []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ledgers[index] = body
}

// failWith makes the node answer every request with HTTP status.
func (n *recordedLedgerNode) failWith(status int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.status = status
}

// mutate rewrites the recorded answer for ledger index through edit, for a
// synthetic variant of a real answer.
func (n *recordedLedgerNode) mutate(index uint64, edit func(result map[string]any)) {
	n.t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	var envelope map[string]any
	if err := json.Unmarshal(n.ledgers[index], &envelope); err != nil {
		n.t.Fatal(err)
	}
	edit(envelope["result"].(map[string]any))
	body, err := json.Marshal(envelope)
	if err != nil {
		n.t.Fatal(err)
	}
	n.ledgers[index] = body
}

// recordedTransaction returns the transaction with hash in a recorded ledger
// result (API v1 shape).
func recordedTransaction(t *testing.T, result map[string]any, hash string) map[string]any {
	t.Helper()
	for _, entry := range result["ledger"].(map[string]any)["transactions"].([]any) {
		tx := entry.(map[string]any)
		if tx["hash"] == hash {
			return tx
		}
	}
	t.Fatalf("transaction %s is not in the recording", hash)
	return nil
}

func scanHashes(transfers []types.DetectedTransfer) []string {
	hashes := make([]string, 0, len(transfers))
	for _, transfer := range transfers {
		hashes = append(hashes, transfer.TxHash)
	}
	return hashes
}

func TestLive_ScanBlock_ReportsANativePaymentWithItsDestinationTag(t *testing.T) {
	t.Parallel()
	node, adapter := newRecordedLedgerNode(t)

	transfers, err := adapter.ScanBlock(context.Background(), ledgerTaggedPayment)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 1 {
		t.Fatalf("want the one Payment and none of the seven offers, got %+v", transfers)
	}
	got := transfers[0]
	closeTime := time.Date(2026, time.October, 11, 9, 53, 11, 0, time.UTC)
	if got.TxHash != taggedPaymentHash || got.From != "rDWd81gTyMMA1arNv9bH1qcBDN4EaozN1Y" ||
		got.To != "rai1iQmERbDPvfuk31XVsYs6ZXeA8QRZnC" || got.Amount.String() != "1000000" ||
		got.Asset != models.NativeXRP || got.Token != nil || got.LogIndex != 7 {
		t.Fatalf("payment %+v", got)
	}
	if got.BlockNumber != ledgerTaggedPayment || got.BlockHash != "28D45A55AB2C0C4EC4F4B6F305D75DE861FD4F675301EC1035622910CD14FB2D" ||
		!got.Timestamp.Equal(closeTime) {
		t.Fatalf("ledger fields %+v", got)
	}
	if got.DestinationTag == nil || *got.DestinationTag != 777001 {
		t.Fatalf("destination tag %v, want 777001", got.DestinationTag)
	}

	requests := node.requests()
	if len(requests) != 1 {
		t.Fatalf("requests %v", requests)
	}
	params := requests[0]
	if params["ledger_index"] != float64(ledgerTaggedPayment) || params["transactions"] != true || params["expand"] != true {
		t.Fatalf("ledger params %v", params)
	}
}

func TestLive_ScanBlock_ReadsTheAPIVersion2Shape(t *testing.T) {
	t.Parallel()
	node, adapter := newRecordedLedgerNode(t)
	v1, err := adapter.ScanBlock(context.Background(), ledgerTaggedPayment)
	if err != nil {
		t.Fatal(err)
	}

	// The same ledger recorded with api_version 2: tx_json and meta per entry, and
	// DeliverMax instead of Amount.
	node.answer(ledgerTaggedPayment, readLedgerFixture(t, "ledger_21451514_api_v2.json"))
	v2, err := adapter.ScanBlock(context.Background(), ledgerTaggedPayment)
	if err != nil {
		t.Fatal(err)
	}
	if len(v1) != 1 || len(v2) != 1 {
		t.Fatalf("v1 %+v v2 %+v", v1, v2)
	}
	a, b := v1[0], v2[0]
	if a.TxHash != b.TxHash || a.From != b.From || a.To != b.To || a.Amount.Cmp(b.Amount) != 0 ||
		a.BlockHash != b.BlockHash || a.LogIndex != b.LogIndex || !a.Timestamp.Equal(b.Timestamp) ||
		b.DestinationTag == nil || *a.DestinationTag != *b.DestinationTag {
		t.Fatalf("api v1 %+v and api v2 %+v differ", a, b)
	}
}

func TestLive_ScanBlock_SkipsFailedPaymentsAndOtherTransactionTypes(t *testing.T) {
	t.Parallel()
	_, adapter := newRecordedLedgerNode(t)

	transfers, err := adapter.ScanBlock(context.Background(), ledgerFailedPayment)
	if err != nil {
		t.Fatal(err)
	}
	// Ledger order (TransactionIndex 8, 9, 10, 18, 21). The tecUNFUNDED_PAYMENT
	// Payment (index 22) and the two CheckCash (7, 20), which also deliver XRP,
	// are not deposits.
	want := []struct {
		hash, to, drops string
		index           uint
	}{
		{"15DFF33BCB07FEF157836ADEFC15D95E05A4B40D67AE6E827D4F547EFA9A4FA8", "rwpNHW66K4YcwAGyx7F1ehUW12Yujwy7Ni", "1468948", 8},
		{"B01DD89B59CD557D61C826C5BC2E3DC85C245BFEB65E9D63656D376B2F31333F", "rHLxnbVNBYufyqumxnXZq5EEVgNqu9UqsA", "19843775", 9},
		{"38ECAED4A20E54AAF7BA3F24D6BA89EC8BF38F4E3AD1DE8D5D5C581A5F067272", "rP1KKcBj3b2yM8rBjw3Rnz5VXeRXkYbHss", "16304463", 10},
		{"12DA648C33819F7E7A7B0A98A8FE41BE529291BBB8F74F5D6BCC2D094FA80F33", "rAzUPtFENQorUVnMNVMqn7bBRQvdtBrR1", "3110000000", 18},
		{"1C622DB8589ED15E64D5B431732887B992D857B78F3E0548BD5AFADEBC33D136", "rhniDcPKVdbuRxeWUWdSuf9a7cP4kFgTXB", "3405387", 21},
	}
	if len(transfers) != len(want) {
		t.Fatalf("got %v, want the five successful native Payments", scanHashes(transfers))
	}
	for i, w := range want {
		got := transfers[i]
		if got.TxHash != w.hash || got.To != w.to || got.Amount.String() != w.drops || got.LogIndex != w.index || got.DestinationTag != nil {
			t.Fatalf("transfer %d = %+v, want %+v", i, got, w)
		}
	}
}

func TestLive_ScanBlock_IgnoresIssuedCurrencyPayments(t *testing.T) {
	t.Parallel()
	_, adapter := newRecordedLedgerNode(t)

	transfers, err := adapter.ScanBlock(context.Background(), ledgerIssuedCurrency)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 1 || transfers[0].TxHash != "3466AB76881E10E3358CB0709310E497767844EF84FE3093542AE67AC25FCB93" ||
		transfers[0].Amount.String() != "100000000" || transfers[0].To != "rnJtMDNjnBgiHaV1kZEQazdsTmG15aVYup" {
		t.Fatalf("want only the native Payment, not the USD one to raACRtW1uoq83PcKaUMeniJZ3VU2ZCLeBm, got %+v", transfers)
	}
}

func TestLive_ScanBlock_CreditsTheDeliveredAmountOfAPartialPayment(t *testing.T) {
	t.Parallel()
	node, adapter := newRecordedLedgerNode(t)

	// The recorded partial payment (tfPartialPayment, inside a Batch) asked for
	// 999999999999999 drops and delivered 896668. Synthetic: it is sent to another
	// account instead of its own sender, the metadata kept as recorded.
	const partialPaymentHash = "8D43D9E759C079244CEE21F423639EF63EA12A5A07A4AFFD0A18ED811AD434EB"
	node.mutate(ledgerSelfPayments, func(result map[string]any) {
		recordedTransaction(t, result, partialPaymentHash)["Destination"] = "rai1iQmERbDPvfuk31XVsYs6ZXeA8QRZnC"
	})
	transfers, err := adapter.ScanBlock(context.Background(), ledgerSelfPayments)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 1 || transfers[0].TxHash != partialPaymentHash || transfers[0].Amount.String() != "896668" ||
		transfers[0].From != "rGP2JwzPqAbfJ2nkyzAcewCkeHpfp3KwS5" || transfers[0].To != "rai1iQmERbDPvfuk31XVsYs6ZXeA8QRZnC" {
		t.Fatalf("want the 896668 drops delivered, not the 999999999999999 asked, got %+v", transfers)
	}
}

func TestLive_ScanBlock_SkipsPaymentsAnAccountMakesToItself(t *testing.T) {
	t.Parallel()
	_, adapter := newRecordedLedgerNode(t)

	// Two of the four Payments deliver XRP (896668 and 616946 drops), but each
	// converts the sender's own issued currency: Account is Destination.
	transfers, err := adapter.ScanBlock(context.Background(), ledgerSelfPayments)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers) != 0 {
		t.Fatalf("self payments reported as deposits: %+v", transfers)
	}
}

func TestLive_ScanBlock_AnEmptyValidatedLedgerHasNoTransfers(t *testing.T) {
	t.Parallel()
	_, adapter := newRecordedLedgerNode(t)

	transfers, err := adapter.ScanBlock(context.Background(), ledgerEmpty)
	if err != nil || len(transfers) != 0 {
		t.Fatalf("empty ledger: %+v, %v", transfers, err)
	}
}

func TestLive_ScanBlock_FailsOnALedgerItCannotRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		ledger uint64
		setup  func(t *testing.T, node *recordedLedgerNode)
		want   string
	}{
		{"open ledger, not validated", ledgerOpen, nil, "not validated"},
		{"ledger not found", ledgerNotFound, nil, "lgrNotFound"},
		{"server unavailable", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.failWith(http.StatusServiceUnavailable)
		}, "http 503"},
		{"rate limited", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.answer(ledgerTaggedPayment, []byte(`{"result":{"error":"slowDown","status":"error"}}`))
		}, "slowDown"},
		{"validated but not closed", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				result["ledger"].(map[string]any)["closed"] = false
			})
		}, "not closed"},
		{"answer for another ledger", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.answer(ledgerTaggedPayment, readLedgerFixture(t, ledgerFixtures[ledgerEmpty]))
		}, "answered ledger 21451524"},
		{"close time before the Ripple epoch", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				result["ledger"].(map[string]any)["close_time"] = -5
			})
		}, "close time"},
		{"result of another shape", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				result["ledger"].(map[string]any)["close_time"] = "soon"
			})
		}, "not the expected shape"},
		{"transactions left out", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				delete(result["ledger"].(map[string]any), "transactions")
			})
		}, "no transactions"},
		{"transactions not expanded", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				result["ledger"].(map[string]any)["transactions"] = []any{taggedPaymentHash}
			})
		}, "not expanded"},
		{"transaction without metadata", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				delete(recordedTransaction(t, result, taggedPaymentHash), "metaData")
			})
		}, "metadata"},
		{"payment without delivered_amount", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				delete(recordedTransaction(t, result, taggedPaymentHash)["metaData"].(map[string]any), "delivered_amount")
			})
		}, "delivered_amount"},
		{"delivered_amount unavailable", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				recordedTransaction(t, result, taggedPaymentHash)["metaData"].(map[string]any)["delivered_amount"] = "unavailable"
			})
		}, "delivered_amount"},
		{"delivered drops as a JSON number", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				recordedTransaction(t, result, taggedPaymentHash)["metaData"].(map[string]any)["delivered_amount"] = 1.5
			})
		}, "delivered_amount"},
		{"payment without a destination", ledgerTaggedPayment, func(t *testing.T, node *recordedLedgerNode) {
			node.mutate(ledgerTaggedPayment, func(result map[string]any) {
				delete(recordedTransaction(t, result, taggedPaymentHash), "Destination")
			})
		}, "destination"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node, adapter := newRecordedLedgerNode(t)
			if tc.setup != nil {
				tc.setup(t, node)
			}
			transfers, err := adapter.ScanBlock(context.Background(), tc.ledger)
			if err == nil {
				t.Fatalf("scan succeeded with %d transfers", len(transfers))
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), fmt.Sprint(tc.ledger)) {
				t.Fatalf("error %q does not name ledger %d and %q", err, tc.ledger, tc.want)
			}
		})
	}
}

func TestLive_ScanBlock_ReadsALedgerLargerThanTheDefaultResponseLimit(t *testing.T) {
	t.Parallel()
	node, adapter := newRecordedLedgerNode(t)

	// Synthetic: the recorded ledger padded past the 1 MiB other calls accept, as a
	// busy mainnet ledger is.
	node.mutate(ledgerTaggedPayment, func(result map[string]any) {
		result["padding"] = strings.Repeat("0", xrpRPCMaxResponseBytes)
	})
	transfers, err := adapter.ScanBlock(context.Background(), ledgerTaggedPayment)
	if err != nil || len(transfers) != 1 {
		t.Fatalf("large ledger: %+v, %v", transfers, err)
	}
}
