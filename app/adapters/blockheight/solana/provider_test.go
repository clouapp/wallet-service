package solana

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/httpclient"
)

func TestProvider_ReadsTheMainnetSlot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, getSlotBody, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":123456789,"id":1}`))
	}))
	t.Cleanup(srv.Close)

	p := New()
	p.client = httpclient.Wrap(srv.Client())
	p.mainnetRPC = srv.URL

	height, err := p.GetBlockHeight(context.Background(), models.ChainSOL)
	require.NoError(t, err)
	assert.Equal(t, uint64(123456789), height)
}

func TestProvider_ReadsTheDevnetSlot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":42,"id":1}`))
	}))
	t.Cleanup(srv.Close)

	p := New()
	p.client = httpclient.Wrap(srv.Client())
	p.devnetRPC = srv.URL

	height, err := p.GetBlockHeight(context.Background(), models.ChainTSOL)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), height)
}

func TestProvider_DefaultsToThePublicRPC(t *testing.T) {
	p := New()
	assert.Equal(t, mainnetRPC, p.mainnetRPC)
	assert.Equal(t, devnetRPC, p.devnetRPC)
}

func TestProvider_RejectsAnUnknownChain(t *testing.T) {
	_, err := New().GetBlockHeight(context.Background(), models.ChainBTC)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown chain_id")
}
