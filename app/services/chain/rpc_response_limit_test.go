package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

// largeBlockTransactions makes a block body past the 1 MiB default (Base Sepolia
// 47639340 is ~4.3 MB), each transaction padded with calldata-sized input.
const (
	largeBlockTransactions = 600
	largeBlockInputBytes   = 4096
)

func largeEVMBlockBody(credited string) string {
	var txs []string
	input := "0x" + strings.Repeat("ab", largeBlockInputBytes)
	for i := 0; i < largeBlockTransactions; i++ {
		txs = append(txs, fmt.Sprintf(`{"hash":"0x%064x","from":"0x%040x","to":"0x%040x","value":"0x0","input":"%s"}`, i, i, i+1, input))
	}
	txs = append(txs, fmt.Sprintf(`{"hash":"0x%064x","from":"0x%040x","to":"%s","value":"0x2386f26fc10000","input":"0x"}`, largeBlockTransactions, 1, credited))
	return `{"jsonrpc":"2.0","id":1,"result":{"hash":"0xb1","timestamp":"0x1","transactions":[` + strings.Join(txs, ",") + `]}}`
}

func serveBody(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEVMScanBlock_AcceptsBlocksAboveTheDefaultLimit(t *testing.T) {
	const credited = "0x00000000000000000000000000000000000000c1"
	body := largeEVMBlockBody(credited)
	if len(body) <= rpcMaxResponseBytes {
		t.Fatalf("fixture is %d bytes, must exceed the %d-byte default", len(body), rpcMaxResponseBytes)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "eth_getLogs" {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":[]}`)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	var out map[string]any
	err := NewRPCClient(srv.URL, "", "").Call(context.Background(), "eth_getBlockByNumber", &out, "0x1", true)
	if err == nil || !strings.Contains(err.Error(), "response larger than") {
		t.Fatalf("default client err = %v, want an explicit size error", err)
	}

	live := NewEVMLive(EVMConfig{ChainIDStr: models.ChainBase, NativeSymbol: models.NativeETH, RPCURL: srv.URL, StrictLogScan: true})
	transfers, err := live.ScanBlock(context.Background(), 1)
	if err != nil {
		t.Fatalf("ScanBlock on a %d-byte block: %v", len(body), err)
	}
	if len(transfers) != 1 || !strings.EqualFold(transfers[0].To, credited) {
		t.Fatalf("transfers = %+v, want the single credit to %s", transfers, credited)
	}
}

func TestEVMRPC_RefusesAnswersAboveItsLimit(t *testing.T) {
	srv := serveBody(t, `{"jsonrpc":"2.0","id":1,"result":"`+strings.Repeat("a", evmRPCMaxResponseBytes)+`"}`)

	live := NewEVMLive(EVMConfig{ChainIDStr: models.ChainBase, NativeSymbol: models.NativeETH, RPCURL: srv.URL})
	_, err := live.ScanBlock(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("response larger than %d bytes", evmRPCMaxResponseBytes)) {
		t.Fatalf("err = %v, want an explicit size error instead of a truncated body", err)
	}
}

func TestSolanaScanBlock_AcceptsBlocksAboveTheDefaultLimit(t *testing.T) {
	padding := strings.Repeat("a", rpcMaxResponseBytes)
	srv := serveBody(t, `{"jsonrpc":"2.0","id":1,"result":{"blockhash":"`+padding+`","blockTime":1,"transactions":[]}}`)

	live := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: srv.URL})
	if _, err := live.ScanBlock(context.Background(), fixtureSlot); err != nil {
		t.Fatalf("ScanBlock above 1 MiB: %v", err)
	}
}
