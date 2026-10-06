package solana

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

const defaultRPCResponseBytes = 1 << 20

func serveBody(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSolanaScanBlock_AcceptsBlocksAboveTheDefaultLimit(t *testing.T) {
	padding := strings.Repeat("a", defaultRPCResponseBytes)
	srv := serveBody(t, `{"jsonrpc":"2.0","id":1,"result":{"blockhash":"`+padding+`","blockTime":1,"transactions":[]}}`)

	live := NewSolanaLive(SolanaConfig{ChainIDStr: models.ChainSOL, NativeSymbol: models.NativeSOL, RPCURL: srv.URL})
	if _, err := live.ScanBlock(context.Background(), fixtureSlot); err != nil {
		t.Fatalf("ScanBlock above 1 MiB: %v", err)
	}
}
