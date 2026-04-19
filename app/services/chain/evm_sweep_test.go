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
