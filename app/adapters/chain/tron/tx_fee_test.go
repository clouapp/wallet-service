package tron

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

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
