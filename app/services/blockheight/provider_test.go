package blockheight

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestSolanaPublicProvider_GetBlockHeight_ValidJSONRPC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":123456789,"id":1}`))
	}))
	defer srv.Close()

	p := NewSolanaPublicProvider()
	p.client = httpclient.Wrap(srv.Client())
	p.mainnetRPC = srv.URL

	height, err := p.GetBlockHeight(context.Background(), "sol")
	require.NoError(t, err)
	assert.Equal(t, uint64(123456789), height)
}

func TestSolanaPublicProvider_GetBlockHeight_UnknownChain(t *testing.T) {
	p := NewSolanaPublicProvider()
	_, err := p.GetBlockHeight(context.Background(), "btc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown chain_id")
}
