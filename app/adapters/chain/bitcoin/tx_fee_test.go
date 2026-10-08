package bitcoin

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
