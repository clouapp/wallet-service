package evm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

func jsonRPCServer(t *testing.T, results map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		key := req.Method
		if len(req.Params) > 0 {
			if _, ok := results[key+":"+string(req.Params[0])]; ok {
				key += ":" + string(req.Params[0])
			}
		}
		result, ok := results[key]
		if !ok {
			t.Errorf("unexpected rpc %s %s", req.Method, body)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":`+result+`}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEVMTransactionFee(t *testing.T) {
	const hash = "0xabc"
	cases := []struct {
		name    string
		results map[string]string
		want    string
	}{
		{
			name:    "gasUsed × effectiveGasPrice",
			results: map[string]string{"eth_getTransactionReceipt": `{"gasUsed":"0x5208","effectiveGasPrice":"0x59682f00"}`},
			want:    "31500000000000", // 21000 × 1.5 gwei
		},
		{
			name:    "OP Stack adds the L1 data fee",
			results: map[string]string{"eth_getTransactionReceipt": `{"gasUsed":"0x5208","effectiveGasPrice":"0x3b9aca00","l1Fee":"0x2710"}`},
			want:    "21000000010000",
		},
		{
			name: "pre-London receipt uses the tx gas price",
			results: map[string]string{
				"eth_getTransactionReceipt": `{"gasUsed":"0x5208"}`,
				"eth_getTransactionByHash":  `{"gasPrice":"0x2"}`,
			},
			want: "42000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := jsonRPCServer(t, tc.results)
			adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainETH, NativeSymbol: "eth", NativeDecimal: 18, RPCURL: srv.URL})
			fee, err := adapter.TransactionFee(context.Background(), hash)
			if err != nil || fee.String() != tc.want {
				t.Fatalf("fee = %v, %v; want %s", fee, err, tc.want)
			}
		})
	}

	srv := jsonRPCServer(t, map[string]string{"eth_getTransactionReceipt": `null`})
	adapter := NewEVMLive(EVMConfig{ChainIDStr: models.ChainETH, NativeSymbol: "eth", NativeDecimal: 18, RPCURL: srv.URL})
	if _, err := adapter.TransactionFee(context.Background(), hash); !errors.Is(err, chain.ErrTransactionFeeUnknown) {
		t.Fatalf("missing receipt err = %v", err)
	}
}
