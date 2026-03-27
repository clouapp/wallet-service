package blockheight

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEtherscanProvider_GetBlockHeight_ValidHex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.True(t, strings.HasPrefix(r.URL.Path, "/v2/api"))
		q := r.URL.Query()
		assert.Equal(t, "1", q.Get("chainid"))
		assert.Equal(t, "proxy", q.Get("module"))
		assert.Equal(t, "eth_blockNumber", q.Get("action"))
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1234ab"}`))
	}))
	defer srv.Close()

	p := NewEtherscanProvider("")
	p.client = srv.Client()
	p.baseURL = srv.URL

	height, err := p.GetBlockHeight(context.Background(), "eth")
	require.NoError(t, err)
	assert.Equal(t, uint64(0x1234ab), height)
}

func TestEtherscanProvider_GetBlockHeight_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"execution reverted"}}`))
	}))
	defer srv.Close()

	p := NewEtherscanProvider("")
	p.client = srv.Client()
	p.baseURL = srv.URL

	_, err := p.GetBlockHeight(context.Background(), "eth")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpc error")
}

func TestEtherscanProvider_GetBlockHeight_UnknownChain(t *testing.T) {
	p := NewEtherscanProvider("")
	_, err := p.GetBlockHeight(context.Background(), "unknown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown chain_id")
}

func TestBlockstreamProvider_GetBlockHeight_ValidInteger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/blocks/tip/height", r.URL.Path)
		_, _ = w.Write([]byte("850000\n"))
	}))
	defer srv.Close()

	p := NewBlockstreamProvider()
	p.client = srv.Client()
	p.mainnetURL = srv.URL + "/api/blocks/tip/height"

	height, err := p.GetBlockHeight(context.Background(), "btc")
	require.NoError(t, err)
	assert.Equal(t, uint64(850000), height)
}

func TestBlockstreamProvider_GetBlockHeight_UnknownChain(t *testing.T) {
	p := NewBlockstreamProvider()
	_, err := p.GetBlockHeight(context.Background(), "eth")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown chain_id")
}

func TestSolanaPublicProvider_GetBlockHeight_ValidJSONRPC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":123456789,"id":1}`))
	}))
	defer srv.Close()

	p := NewSolanaPublicProvider()
	p.client = srv.Client()
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
