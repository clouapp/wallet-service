package bitcoin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

func TestBroadcastUnknownOutcomeIsNotAKnownFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("closed server was dialed")
	}))
	endpoint := srv.URL
	srv.Close()

	t.Run("rpc", func(t *testing.T) {
		live := NewBitcoinLive(BitcoinConfig{RPCURL: endpoint, NativeSymbol: "BTC"})
		assertUnknownBroadcast(t, live)
	})
	t.Run("rest", func(t *testing.T) {
		live := NewBitcoinLive(BitcoinConfig{RPCURL: endpoint, NativeSymbol: "BTC"})
		live.restAPI = true
		live.http = httpclient.NewClient(time.Second)
		assertUnknownBroadcast(t, live)
	})
}

func TestBroadcastServerErrorStaysAKnownFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	live := NewBitcoinLive(BitcoinConfig{RPCURL: srv.URL, NativeSymbol: "BTC"})
	live.restAPI = true
	live.http = httpclient.Wrap(srv.Client())
	_, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: []byte{0x01}})
	if !chain.KnownFailure(err) || errors.Is(err, chain.ErrUnknownOutcome) {
		t.Fatalf("HTTP 502 broadcast: %v", err)
	}
}

func assertUnknownBroadcast(t *testing.T, live *BitcoinLive) {
	t.Helper()
	_, err := live.BroadcastTransaction(context.Background(), &types.SignedTx{RawBytes: []byte{0x01}})
	if !errors.Is(err, chain.ErrUnknownOutcome) || chain.KnownFailure(err) {
		t.Fatalf("broadcast err %v", err)
	}
}
