package bitcoin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

func TestREST_Errors_OmitTheRPCURL(t *testing.T) {
	const secret = "secret-key"
	const pathSecret = "secret-path"
	live := NewBitcoinLive(BitcoinConfig{RPCURL: "http://user:" + secret + "@127.0.0.1:1/" + pathSecret, NativeSymbol: "BTC"})

	_, transportErr := live.getBalanceREST(context.Background(), "bc1qxy")
	if transportErr == nil || strings.Contains(transportErr.Error(), secret) || strings.Contains(transportErr.Error(), pathSecret) {
		t.Fatalf("transport err %v", transportErr)
	}

	var endpoint string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("down " + endpoint))
	}))
	t.Cleanup(srv.Close)
	endpoint = srv.URL + "/" + pathSecret + "?api_key=" + secret
	echo := NewBitcoinLive(BitcoinConfig{RPCURL: endpoint, NativeSymbol: "BTC"})
	echo.http = httpclient.Wrap(srv.Client())

	_, bodyErr := echo.getBalanceREST(context.Background(), "bc1qxy")
	if bodyErr == nil || strings.Contains(bodyErr.Error(), secret) || strings.Contains(bodyErr.Error(), pathSecret) {
		t.Fatalf("body err %v", bodyErr)
	}

	_, broadcastErr := echo.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: []byte{0x01}})
	if broadcastErr == nil || strings.Contains(broadcastErr.Error(), secret) || strings.Contains(broadcastErr.Error(), pathSecret) {
		t.Fatalf("broadcast err %v", broadcastErr)
	}
}
