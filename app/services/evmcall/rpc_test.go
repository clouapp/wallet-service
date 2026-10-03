package evmcall

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newRPCServer(t *testing.T, results map[string]string) *JSONRPC {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &request)
		result, ok := results[request.Method]
		if !ok {
			t.Errorf("unexpected method %s", request.Method)
			result = "null"
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + result + `}`))
	}))
	t.Cleanup(server.Close)
	rpc, err := NewJSONRPC(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return rpc
}

func TestJSONRPC_DecodesNodeAnswers(t *testing.T) {
	rpc := newRPCServer(t, map[string]string{
		"eth_chainId":               `"0xaa36a7"`,
		"eth_getCode":               `"0x"`,
		"eth_getTransactionCount":   `"0x3"`,
		"eth_estimateGas":           `"0x16b89"`,
		"eth_call":                  `"0x000000000000000000000000000000000000000000000000000000000021961a"`,
		"eth_getTransactionReceipt": `{"status":"0x1","blockNumber":"0xb48d98","gasUsed":"0x1641e","effectiveGasPrice":"0x8094c7ba"}`,
		"eth_getTransactionByHash":  `null`,
	})
	ctx := context.Background()
	if chainID, err := rpc.ChainID(ctx); err != nil || chainID != testChainID {
		t.Fatalf("chain id %d %v", chainID, err)
	}
	if code, err := rpc.Code(ctx, testInbox); err != nil || len(code) != 0 {
		t.Fatalf("empty code %x %v", code, err)
	}
	if nonce, err := rpc.Nonce(ctx, testFrom, blockPending); err != nil || nonce != 3 {
		t.Fatalf("nonce %d %v", nonce, err)
	}
	if estimate, err := rpc.EstimateGas(ctx, CallMsg{From: testFrom, To: testInbox}); err != nil || estimate != testEstimate {
		t.Fatalf("estimate %d %v", estimate, err)
	}
	if result, err := rpc.Call(ctx, CallMsg{From: testFrom, To: testInbox}); err != nil || len(result) != 32 {
		t.Fatalf("call %x %v", result, err)
	}
	receipt, err := rpc.Receipt(ctx, testSignedHash)
	if err != nil || !receipt.Succeeded() || receipt.BlockNumber != 11832728 || receipt.GasUsed != 91_166 || receipt.EffectiveGasPrice == nil {
		t.Fatalf("receipt %+v %v", receipt, err)
	}
	if known, err := rpc.TransactionKnown(ctx, testSignedHash); err != nil || known {
		t.Fatalf("known %t %v", known, err)
	}
}

func TestJSONRPC_AnUnminedTransactionHasNoReceipt(t *testing.T) {
	rpc := newRPCServer(t, map[string]string{"eth_getTransactionReceipt": `null`})
	if receipt, err := rpc.Receipt(context.Background(), testSignedHash); err != nil || receipt != nil {
		t.Fatalf("receipt %+v %v", receipt, err)
	}
}

func TestNewJSONRPC_RequiresHTTP(t *testing.T) {
	for _, url := range []string{"", "ws://node", "SEPOLIA_RPC_URL"} {
		if _, err := NewJSONRPC(url); err == nil {
			t.Errorf("%q: expected an error", url)
		}
	}
}
