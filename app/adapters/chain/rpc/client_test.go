package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/services/chain"
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

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
	var result string
	err := rpc.Call(context.Background(), "eth_blockNumber", &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "0x1234" {
		t.Errorf("expected 0x1234, got %s", result)
	}
}

func TestRPCClient_Call_SendsExplicitUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "Macro-Wallets/0.1 RPC" {
			t.Fatalf("User-Agent = %q", got)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "0x1",
		})
	}))
	defer server.Close()

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
	var result string
	if err := rpc.Call(context.Background(), "eth_blockNumber", &result); err != nil {
		t.Fatal(err)
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

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
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

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
	var result string
	err := rpc.Call(context.Background(), "nonexistent_method", &result)
	if err == nil {
		t.Fatal("expected RPC error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 || err.Error() != "provider error" || strings.Contains(err.Error(), "method not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRPCClient_Call_HTTPErrorIncludesStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
	var result string
	err := rpc.Call(context.Background(), "eth_blockNumber", &result)
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("expected HTTP status error, got %v", err)
	}
}

func TestRPCClient_Call_EmptyUserAndPasswordSendNoBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok || r.Header.Get("Authorization") != "" {
			t.Fatal("authorization was sent")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1, "result": "ok",
		})
	}))
	defer server.Close()

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
	var result string
	if err := rpc.Call(context.Background(), "ping", &result); err != nil {
		t.Fatal(err)
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

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL, User: "btcuser", Password: "btcpass"})
	var result int
	err := rpc.Call(context.Background(), "getblockcount", &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 850000 {
		t.Errorf("expected 850000, got %d", result)
	}
}

// closedPortURL is http://127.0.0.1:<port> on a port that was just free, so the
// connection is refused at once (127.0.0.1:1 hangs until the timeout on WSL2).
func closedPortURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a local port: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return "http://" + addr
}

func TestRPCClient_Call_ConnectionRefused(t *testing.T) {
	rpc := NewRPCClient(RPCClientDeps{URL: closedPortURL(t)})
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

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
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

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
	// Pass nil output — should not panic
	err := rpc.Call(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRPC_Client_IncrementingIDs(t *testing.T) {
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

	rpc := NewRPCClient(RPCClientDeps{URL: server.URL})
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

func TestCall_AJSONRPCErrorCarriedByANon2xxStatusIsTheRPCError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		code    int
		unknown bool
	}{
		{"a rejection with HTTP 500 (bitcoind before v28)", http.StatusInternalServerError, `{"result":null,"error":{"code":-26,"message":"txn-mempool-conflict"},"id":1}`, -26, false},
		{"invalid params with HTTP 400", http.StatusBadRequest, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid argument 0"}}`, -32602, false},
		{"a generic upstream error stays the HTTP failure", http.StatusBadGateway, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"upstream request timeout"}}`, 0, true},
		{"no JSON-RPC answer stays the HTTP failure", http.StatusInternalServerError, `<html>oops</html>`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(node.Close)

			err := NewRPCClient(RPCClientDeps{URL: node.URL}).Call(context.Background(), "sendrawtransaction", nil, "00")
			var rpcErr *RPCError
			if tc.code != 0 {
				if !errors.As(err, &rpcErr) || rpcErr.Code != tc.code {
					t.Fatalf("err = %v, want JSON-RPC error %d", err, tc.code)
				}
			} else if errors.As(err, &rpcErr) {
				t.Fatalf("err = %v, want the HTTP failure", err)
			}
			if got := errors.Is(chain.ClassifyBroadcast(err), chain.ErrUnknownOutcome); got != tc.unknown {
				t.Fatalf("unknown outcome = %v, want %v (err %v)", got, tc.unknown, err)
			}
		})
	}
}
