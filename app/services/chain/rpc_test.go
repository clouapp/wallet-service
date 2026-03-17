package chain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRPCClient_Call_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)

		if req["method"] != "eth_blockNumber" {
			t.Errorf("expected eth_blockNumber, got %v", req["method"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"], "result": "0x1234",
		})
	}))
	defer server.Close()

	rpc := NewRPCClient(server.URL, "", "")
	var result string
	err := rpc.Call(context.Background(), "eth_blockNumber", &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "0x1234" {
		t.Errorf("expected 0x1234, got %s", result)
	}
}

func TestRPCClient_Call_WithParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)

		params := req["params"].([]interface{})
		if len(params) != 2 {
			t.Errorf("expected 2 params, got %d", len(params))
		}
		if params[0] != "0xaddr" {
			t.Errorf("expected 0xaddr, got %v", params[0])
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"], "result": "0xde0b6b3a7640000",
		})
	}))
	defer server.Close()

	rpc := NewRPCClient(server.URL, "", "")
	var result string
	err := rpc.Call(context.Background(), "eth_getBalance", &result, "0xaddr", "latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "0xde0b6b3a7640000" {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestRPCClient_Call_RPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"],
			"error": map[string]interface{}{"code": -32601, "message": "method not found"},
		})
	}))
	defer server.Close()

	rpc := NewRPCClient(server.URL, "", "")
	var result string
	err := rpc.Call(context.Background(), "nonexistent_method", &result)
	if err == nil {
		t.Fatal("expected RPC error")
	}
	if err.Error() != "RPC error -32601: method not found" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRPCClient_Call_BasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "btcuser" || pass != "btcpass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"], "result": 850000,
		})
	}))
	defer server.Close()

	rpc := NewRPCClient(server.URL, "btcuser", "btcpass")
	var result int
	err := rpc.Call(context.Background(), "getblockcount", &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 850000 {
		t.Errorf("expected 850000, got %d", result)
	}
}

func TestRPCClient_Call_ConnectionRefused(t *testing.T) {
	rpc := NewRPCClient("http://localhost:1/invalid", "", "")
	var result string
	err := rpc.Call(context.Background(), "test", &result)
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestRPCClient_Call_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	rpc := NewRPCClient(server.URL, "", "")
	var result string
	err := rpc.Call(context.Background(), "test", &result)
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestRPCClient_Call_NilOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"], "result": "ok",
		})
	}))
	defer server.Close()

	rpc := NewRPCClient(server.URL, "", "")
	// Pass nil output — should not panic
	err := rpc.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRPCClient_IncrementingIDs(t *testing.T) {
	var ids []float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		ids = append(ids, req["id"].(float64))
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req["id"], "result": "ok",
		})
	}))
	defer server.Close()

	rpc := NewRPCClient(server.URL, "", "")
	rpc.Call(context.Background(), "a", nil)
	rpc.Call(context.Background(), "b", nil)
	rpc.Call(context.Background(), "c", nil)

	if len(ids) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Errorf("IDs should be incrementing: %v", ids)
		}
	}
}
