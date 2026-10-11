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
	"github.com/macrowallets/waas/app/services/chain"
)

func TestEVM_FinalizedBlock_ReadsTheFinalizedTagHeader(t *testing.T) {
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
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"number":"0x2f97a26","hash":"0xabc"}}`)
	}))
	t.Cleanup(srv.Close)
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainTPolygon, RPCURL: srv.URL})

	block, err := adapter.FinalizedBlock(context.Background())
	if err != nil || block != 49904166 {
		t.Fatalf("finalized block %d, %v", block, err)
	}
	if len(params) != 2 || string(params[0]) != `"finalized"` || string(params[1]) != `false` {
		t.Fatalf("params %s, want [\"finalized\", false]", params)
	}
}

func TestEVM_FinalizedBlock_FailsWithoutAFinalizedBlock(t *testing.T) {
	cases := map[string]string{
		"null result":     `{"jsonrpc":"2.0","id":1,"result":null}`,
		"no number":       `{"jsonrpc":"2.0","id":1,"result":{"hash":"0xabc"}}`,
		"invalid number":  `{"jsonrpc":"2.0","id":1,"result":{"number":"0xzz"}}`,
		"node error":      `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"finalized block not found"}}`,
		"unknown tag":     `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid argument 0: hex string without 0x prefix"}}`,
		"genesis instead": `{"jsonrpc":"2.0","id":1,"result":{"number":"0x0"}}`,
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, answer)
			}))
			t.Cleanup(srv.Close)
			adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainBSC, RPCURL: srv.URL})
			block, err := adapter.FinalizedBlock(context.Background())
			if err == nil {
				t.Fatalf("finalized block %d accepted", block)
			}
			if strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("error leaks the endpoint: %v", err)
			}
		})
	}
}

func TestEVM_FinalityPolicy_IsTheChainRowPolicy(t *testing.T) {
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainPolygon, Finality: models.FinalityFinalizedTag})
	if got := adapter.FinalityPolicy(); got != models.FinalityFinalizedTag {
		t.Fatalf("policy %q", got)
	}
	if got := chain.FinalityPolicyOf(adapter.WithFeePolicy(chain.FeePolicy{})); got != models.FinalityFinalizedTag {
		t.Fatalf("a fee-scoped adapter reports %q", got)
	}
	if got := chain.FinalityPolicyOf(NewEVMLive(EVMConfig{ChainIDStr: models.ChainETH})); got != models.FinalityConfirmations {
		t.Fatalf("a row without a policy reports %q", got)
	}
}
