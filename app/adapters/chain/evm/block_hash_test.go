package evm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestEVM_BlockHashAt_ReadsTheHeaderOfTheHeight(t *testing.T) {
	var params []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req.Method != "eth_getBlockByNumber" {
			t.Errorf("unexpected rpc %s", req.Method)
		}
		params = req.Params
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"number":"0xb62506","hash":"0xAB12cd"}}`)
	}))
	t.Cleanup(srv.Close)
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainTETH, RPCURL: srv.URL})

	hash, err := adapter.BlockHashAt(context.Background(), 0xb62506)

	if err != nil || hash != "0xab12cd" {
		t.Fatalf("hash %q err %v, want the header's hash in lowercase", hash, err)
	}
	if len(params) != 2 || string(params[0]) != `"0xb62506"` || string(params[1]) != `false` {
		t.Fatalf("params %s, want the height without transactions", params)
	}
}

func TestEVM_BlockHashAt_NoBlockIsAnError(t *testing.T) {
	cases := map[string]string{
		"null result":  `{"jsonrpc":"2.0","id":1,"result":null}`,
		"no hash":      `{"jsonrpc":"2.0","id":1,"result":{"number":"0x64"}}`,
		"other height": `{"jsonrpc":"2.0","id":1,"result":{"number":"0x65","hash":"0xab"}}`,
		"node error":   `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"header not found"}}`,
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, answer)
			}))
			t.Cleanup(srv.Close)
			adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainTETH, RPCURL: srv.URL})

			hash, err := adapter.BlockHashAt(context.Background(), 100)

			if err == nil || hash != "" {
				t.Fatalf("hash %q accepted, want an error: a missing block proves nothing", hash)
			}
			if strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("error leaks the endpoint: %v", err)
			}
		})
	}
}
