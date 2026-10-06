package solana

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
)

const fixtureSlot = 506367800

type recordedRPCRequest struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

// serveSolanaFixture answers every RPC call with a devnet response recorded in
// testdata/solana and keeps the requests it received.
func serveSolanaFixture(t *testing.T, fixture string) (*SolanaLive, *[]recordedRPCRequest) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "solana", fixture))
	if err != nil {
		t.Fatal(err)
	}
	var requests []recordedRPCRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Error(readErr)
			return
		}
		var req recordedRPCRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Error(err)
			return
		}
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: srv.URL}), &requests
}

func TestSolana_ScanBlock_DetectsNativeCreditsFromRecordedBlock(t *testing.T) {
	live, requests := serveSolanaFixture(t, "getBlock_transfers.json")

	transfers, err := live.ScanBlock(context.Background(), fixtureSlot)
	if err != nil {
		t.Fatal(err)
	}

	want := []struct {
		tx, from, to string
		amount       int64
		index        uint
	}{
		{"41iqE5xg9ttZAk1uZkirZsQz3GG1DJ3kcU33YUFJAyraS3MFG2BWMxweU7KUXqhDZuUmzW2bqs5L3PPE1RRQEK9r",
			"AMbsiP9F8YY2y8n9uFdqtw7yNZZHvTWFEWSQGHKtmkoQ", "AJor34TKjdm2pAK6V7nkBmKNpnARHL7CkiwnBVTE9J6b", 150_000_000, 1},
		{"2GZVo5ebJkoVCux2P4tW5Eae68HdJtBz8my2Wa2hwz43LahUhNvZpQ26sqbshnf3w3Xhv1SYghbqm3KEX2kGnpQc",
			"DFGFFAAeMg3CX3UMcMpBsc15KtXy4kVHLLvrTLBB8cSz", "FW5x1ZS3c1c8BDcoru1ryZpqsoYPuVHbpC7mLWZJVqgj", 1_488_440, 2},
		{"2GZVo5ebJkoVCux2P4tW5Eae68HdJtBz8my2Wa2hwz43LahUhNvZpQ26sqbshnf3w3Xhv1SYghbqm3KEX2kGnpQc",
			"DFGFFAAeMg3CX3UMcMpBsc15KtXy4kVHLLvrTLBB8cSz", "4dJgNbQANPeevr1B51hz99DZzbzmtAB55eDz4jzqZL9H", 1_488_440, 3},
	}
	if len(transfers) != len(want) {
		t.Fatalf("want %d transfers (failed transactions skipped), got %d: %+v", len(want), len(transfers), transfers)
	}
	for i, w := range want {
		got := transfers[i]
		if got.TxHash != w.tx || got.From != w.from || got.To != w.to || got.Amount.Int64() != w.amount || got.LogIndex != w.index {
			t.Fatalf("transfer %d: got %+v, want %+v", i, got, w)
		}
		if got.BlockNumber != fixtureSlot || got.BlockHash != "Braetyz9fCW1P6ahjpxpBqLVDCh61kZPSNgZwx8okPt2" || got.Asset != models.NativeSOL {
			t.Fatalf("transfer %d block fields: %+v", i, got)
		}
		if !got.Timestamp.Equal(time.Unix(1790880055, 0)) {
			t.Fatalf("transfer %d timestamp %s", i, got.Timestamp)
		}
	}

	if len(*requests) != 1 || (*requests)[0].Method != "getBlock" {
		t.Fatalf("unexpected requests %+v", *requests)
	}
	var opts map[string]interface{}
	if err := json.Unmarshal((*requests)[0].Params[1], &opts); err != nil {
		t.Fatal(err)
	}
	if opts["transactionDetails"] != "accounts" || opts["maxSupportedTransactionVersion"] != float64(1) ||
		opts["commitment"] != "finalized" || opts["rewards"] != false {
		t.Fatalf("getBlock options %v", opts)
	}
}

func TestSolana_ScanBlock_SkippedSlotYieldsNoTransfers(t *testing.T) {
	live, _ := serveSolanaFixture(t, "getBlock_skipped.json")
	transfers, err := live.ScanBlock(context.Background(), 506348904)
	if err != nil {
		t.Fatalf("skipped slot must not stall the scanner: %v", err)
	}
	if len(transfers) != 0 {
		t.Fatalf("transfers %+v", transfers)
	}
}

func TestSolana_ScanBlock_OtherRPCErrorsPropagate(t *testing.T) {
	for _, code := range []int{-32004, -32015, -32603} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": 1, "error": map[string]interface{}{"code": code, "message": "boom"},
			})
		}))
		live := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, RPCURL: srv.URL})
		if _, err := live.ScanBlock(context.Background(), 1); err == nil {
			t.Fatalf("code %d must be retried, not skipped", code)
		}
		srv.Close()
	}
}

func TestSolana_NativeCredits_IgnoresMisalignedBalances(t *testing.T) {
	block := &solanaBlockAccounts{Transactions: []solanaBlockTransaction{{}}}
	block.Transactions[0].Transaction.Signatures = []string{"sig"}
	block.Transactions[0].Transaction.AccountKeys = []solanaBlockAccountKey{{Pubkey: "payer", Signer: true}, {Pubkey: "dest"}}
	block.Transactions[0].Meta = &struct {
		Err          interface{} `json:"err"`
		PreBalances  []uint64    `json:"preBalances"`
		PostBalances []uint64    `json:"postBalances"`
	}{PreBalances: []uint64{10}, PostBalances: []uint64{5, 5}}
	if got := solanaNativeCredits(1, block, models.NativeSOL); len(got) != 0 {
		t.Fatalf("misaligned balances must be ignored, got %+v", got)
	}
}

func TestSolana_GetTransactionBlock_RecordedStatuses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "solana", "getSignatureStatuses.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Result struct {
			Context json.RawMessage   `json:"context"`
			Value   []json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &recorded); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		status    json.RawMessage
		wantSlot  uint64
		wantError bool
	}{
		{"finalized success", recorded.Result.Value[0], fixtureSlot, false},
		{"finalized failure", recorded.Result.Value[1], 0, true},
		{"unknown signature", recorded.Result.Value[2], 0, false},
		{"confirmed but not finalized", json.RawMessage(`{"slot":7,"err":null,"confirmationStatus":"confirmed","confirmations":3}`), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var method string
			var params []json.RawMessage
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req recordedRPCRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				method, params = req.Method, req.Params
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"context":`+string(recorded.Result.Context)+`,"value":[`+string(tc.status)+`]}}`)
			}))
			defer srv.Close()
			live := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, RPCURL: srv.URL})

			slot, err := live.GetTransactionBlock(context.Background(), "sig")
			if (err != nil) != tc.wantError {
				t.Fatalf("err %v, wantError %v", err, tc.wantError)
			}
			if slot != tc.wantSlot {
				t.Fatalf("slot %d, want %d", slot, tc.wantSlot)
			}
			if method != "getSignatureStatuses" || len(params) != 2 || string(params[1]) != `{"searchTransactionHistory":true}` {
				t.Fatalf("request %s %s", method, params)
			}
		})
	}
}

func TestSolana_Native_TransferReserve(t *testing.T) {
	var method string
	var params []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req recordedRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		method, params = req.Method, req.Params
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":890880}`)
	}))
	defer srv.Close()

	fee, minimumRemaining, err := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, RPCURL: srv.URL}).NativeTransferReserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fee.Int64() != solanaNativeFeeLamports || minimumRemaining.Int64() != 890880 {
		t.Fatalf("fee %s minimum %s", fee, minimumRemaining)
	}
	if method != "getMinimumBalanceForRentExemption" || len(params) != 2 || string(params[0]) != "0" {
		t.Fatalf("request %s %s", method, params)
	}
}

func TestSolana_GetTransactionBlock_RequiresSignature(t *testing.T) {
	if _, err := (&SolanaLive{}).GetTransactionBlock(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}
