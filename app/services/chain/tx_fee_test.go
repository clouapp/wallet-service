package chain

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

// jsonRPCServer answers each method with a canned result (a JSON value).
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
	if _, err := adapter.TransactionFee(context.Background(), hash); !errors.Is(err, ErrTransactionFeeUnknown) {
		t.Fatalf("missing receipt err = %v", err)
	}
}

func TestSolanaTransactionFee(t *testing.T) {
	adapter := fakeSolanaRPC(t, map[string]string{"getTransaction": `"result":{"slot":1,"meta":{"fee":5000,"err":null}}`})
	fee, err := adapter.TransactionFee(context.Background(), "5sig")
	if err != nil || fee.Int64() != 5000 {
		t.Fatalf("fee = %v, %v", fee, err)
	}
	missing := fakeSolanaRPC(t, map[string]string{"getTransaction": `"result":null`})
	if _, err := missing.TransactionFee(context.Background(), "5sig"); !errors.Is(err, ErrTransactionFeeUnknown) {
		t.Fatalf("missing tx err = %v", err)
	}
}

func TestTronTransactionFee(t *testing.T) {
	const txID = "dd5e5b3f8d2e8a4f1c2b3a4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7081"
	cases := []struct {
		name string
		info string
		want int64
	}{
		{"burned fee", `{"id":"` + txID + `","blockNumber":5,"fee":345000,"receipt":{"energy_fee":0,"net_fee":345000,"result":"SUCCESS"}}`, 345000},
		{"free bandwidth", `{"id":"` + txID + `","blockNumber":5,"receipt":{"net_usage":268}}`, 0},
		{"receipt only", `{"id":"` + txID + `","blockNumber":5,"receipt":{"energy_fee":4200000,"net_fee":345000}}`, 4545000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wallet/gettransactioninfobyid" {
					http.NotFound(w, r)
					return
				}
				_, _ = io.WriteString(w, tc.info)
			}))
			defer srv.Close()
			adapter := NewTronLive(TronConfig{ChainIDStr: models.ChainTron, NativeSymbol: models.NativeTRX, RPCURL: srv.URL})
			fee, err := adapter.TransactionFee(context.Background(), txID)
			if err != nil || fee.Int64() != tc.want {
				t.Fatalf("fee = %v, %v; want %d", fee, err, tc.want)
			}
		})
	}
}

func TestBitcoinTransactionFee_EsploraAndFailover(t *testing.T) {
	primary, primarySrv := newFailoverEsplora(t)
	primary.down.Store(true)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/block-height/0":
			_, _ = io.WriteString(w, ltcTestnetGenesisHash)
		case "/tx/" + failoverTestUTXOTx:
			_, _ = io.WriteString(w, `{"txid":"`+failoverTestUTXOTx+`","fee":141,"status":{"confirmed":true}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fallback.Close()
	live := newFailoverTestLive(t, primarySrv.URL, fallback.URL)

	fee, err := live.TransactionFee(context.Background(), failoverTestUTXOTx)
	if err != nil || fee.Int64() != 141 {
		t.Fatalf("fee = %v, %v; want 141 from the fallback", fee, err)
	}
}

func TestBitcoinTransactionFee_JSONRPCFromPrevouts(t *testing.T) {
	prevID := strings.Repeat("1", 64)
	srv := jsonRPCServer(t, map[string]string{
		`getrawtransaction:"` + failoverTestUTXOTx + `"`: `{"vin":[{"txid":"` + prevID + `","vout":0}],"vout":[{"value":0.0005,"n":0},{"value":0.00049859,"n":1}]}`,
		`getrawtransaction:"` + prevID + `"`:             `{"vin":[{"txid":"` + strings.Repeat("2", 64) + `","vout":3}],"vout":[{"value":0.00100000,"n":0}]}`,
	})
	live := NewBitcoinLive(BitcoinConfig{ChainIDStr: models.ChainTLTC, NativeSymbol: models.NativeLTC, RPCURL: srv.URL, IsTestnet: true})
	fee, err := live.TransactionFee(context.Background(), failoverTestUTXOTx)
	if err != nil || fee.Int64() != 141 {
		t.Fatalf("fee = %v, %v; want 100000 − 50000 − 49859 = 141", fee, err)
	}
}

func TestElectrumTransactionFeeFromPrevouts(t *testing.T) {
	fake, rawURL := newTCPFakeElectrum(t)
	prevID := strings.Repeat("1", 64)
	fake.handle("blockchain.transaction.get", func(params []json.RawMessage) (any, *electrumError) {
		switch string(params[0]) {
		case `"` + failoverTestUTXOTx + `"`:
			return map[string]any{"vin": []map[string]any{{"txid": prevID, "vout": 1}}, "vout": []map[string]any{{"value": 0.01099822, "n": 0}}}, nil
		case `"` + prevID + `"`:
			return map[string]any{"vout": []map[string]any{{"value": 0.5, "n": 0}, {"value": 0.0110, "n": 1}}}, nil
		}
		return nil, &electrumError{Code: 2, Message: electrumTxNotFound}
	})
	provider := ltcTestnetElectrum(t, rawURL)
	fee, err := provider.transactionFee(context.Background(), failoverTestUTXOTx)
	if err != nil || fee != 178 {
		t.Fatalf("fee = %d, %v; want 1100000 − 1099822 = 178", fee, err)
	}
}
