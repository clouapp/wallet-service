package chain

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEVMGasReadinessThreshold verifies the getter wiring. Tasks 13+ populate cfg.
func TestEVMGasReadinessThreshold(t *testing.T) {
	got := (&EVMLive{cfg: EVMConfig{GasReadinessThreshold: big.NewInt(5_000_000_000_000_000)}}).GasReadinessThreshold()
	if got == nil || got.String() != "5000000000000000" {
		t.Fatalf("expected 5000000000000000, got %v", got)
	}
	if nilGot := (&EVMLive{cfg: EVMConfig{}}).GasReadinessThreshold(); nilGot != nil {
		t.Fatalf("expected nil when unset, got %v", nilGot)
	}
}

// TestEVMEstimateGasPrice proves the adapter returns the raw wei-valued price
// fetched from `eth_gasPrice` so the sweep planner can multiply by the
// hardcoded gas limits without an additional conversion step.
func TestEVMEstimateGasPrice(t *testing.T) {
	var seenMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seenMethod, _ = req["method"].(string)
		// 0x4a817c800 = 20_000_000_000 wei = 20 gwei.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"], "result": "0x4a817c800",
		})
	}))
	defer server.Close()

	adapter := NewEVMLive(EVMConfig{ChainIDStr: "eth", RPCURL: server.URL})
	got, err := adapter.EstimateGasPrice(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenMethod != "eth_gasPrice" {
		t.Fatalf("expected RPC method eth_gasPrice, got %q", seenMethod)
	}
	want := big.NewInt(20_000_000_000)
	if got == nil || got.Cmp(want) != 0 {
		t.Fatalf("expected %s, got %v", want.String(), got)
	}
}

// TestEVMEstimateGasPrice_PropagatesRPCError ensures the adapter surfaces
// transport / RPC errors so the sweep planner can decide to fall back to a
// nil EstimatedGas rather than silently pretending the price is 0.
func TestEVMEstimateGasPrice_PropagatesRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"],
			"error": map[string]interface{}{"code": -32000, "message": "rpc down"},
		})
	}))
	defer server.Close()

	adapter := NewEVMLive(EVMConfig{ChainIDStr: "eth", RPCURL: server.URL})
	got, err := adapter.EstimateGasPrice(context.Background())
	if err == nil {
		t.Fatal("expected error from RPC failure")
	}
	if got != nil {
		t.Fatalf("expected nil on error, got %s", got.String())
	}
}

// TestEVMDustThresholdNativeVsToken verifies native vs token asset routing.
func TestEVMDustThresholdNativeVsToken(t *testing.T) {
	adapter := &EVMLive{cfg: EVMConfig{
		NativeSymbol:        "eth",
		DustThresholdNative: big.NewInt(500_000_000_000_000),
	}}
	if got := adapter.DustThreshold("eth"); got == nil || got.String() != "500000000000000" {
		t.Fatalf("native: expected 500000000000000, got %v", got)
	}
	if got := adapter.DustThreshold("usdt"); got != nil {
		t.Fatalf("token: expected nil, got %v", got)
	}
}

// TestEVMGetTransactionBlock_Mined proves the adapter decodes the blockNumber
// field returned by `eth_getTransactionByHash` so the confirmation loop can
// reconcile sweep / withdrawal / gas_seed rows that were inserted with
// block_number=0. The JSON shape mirrors what go-ethereum / Infura return.
func TestEVMGetTransactionBlock_Mined(t *testing.T) {
	var seenMethod string
	var seenHash string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seenMethod, _ = req["method"].(string)
		if params, ok := req["params"].([]interface{}); ok && len(params) > 0 {
			seenHash, _ = params[0].(string)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]interface{}{
				"hash":        "0xdeadbeef",
				"blockNumber": "0x64",
				"from":        "0xfrom", "to": "0xto",
			},
		})
	}))
	defer server.Close()

	adapter := NewEVMLive(EVMConfig{ChainIDStr: "eth", RPCURL: server.URL})
	block, err := adapter.GetTransactionBlock(context.Background(), "0xdeadbeef")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenMethod != "eth_getTransactionByHash" {
		t.Fatalf("expected eth_getTransactionByHash, got %q", seenMethod)
	}
	if seenHash != "0xdeadbeef" {
		t.Fatalf("expected hash param 0xdeadbeef, got %q", seenHash)
	}
	if block != 100 {
		t.Fatalf("expected block 100 (0x64), got %d", block)
	}
}

// TestEVMGetTransactionBlock_Pending covers both "not yet mined" shapes the
// node can return: the whole result is null (tx unknown to this node) and the
// tx exists but blockNumber is null. Both must return (0, nil) so the
// confirmation loop treats the row as still pending and retries next tick.
func TestEVMGetTransactionBlock_Pending(t *testing.T) {
	tests := []struct {
		name   string
		result interface{}
	}{
		{"result null", nil},
		{"blockNumber null", map[string]interface{}{"hash": "0xabc", "blockNumber": nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&req)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0", "id": req["id"], "result": tt.result,
				})
			}))
			defer server.Close()

			adapter := NewEVMLive(EVMConfig{ChainIDStr: "eth", RPCURL: server.URL})
			block, err := adapter.GetTransactionBlock(context.Background(), "0xabc")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if block != 0 {
				t.Fatalf("expected 0 for pending, got %d", block)
			}
		})
	}
}

// TestEVMGetTransactionBlock_RPCError surfaces RPC-level failures so the
// confirmation loop can skip and retry rather than silently flipping the row
// to confirmed with a bogus block number.
func TestEVMGetTransactionBlock_RPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"],
			"error": map[string]interface{}{"code": -32000, "message": "rpc down"},
		})
	}))
	defer server.Close()

	adapter := NewEVMLive(EVMConfig{ChainIDStr: "eth", RPCURL: server.URL})
	block, err := adapter.GetTransactionBlock(context.Background(), "0xabc")
	if err == nil {
		t.Fatal("expected error from RPC failure")
	}
	if block != 0 {
		t.Fatalf("expected 0 on error, got %d", block)
	}
}
