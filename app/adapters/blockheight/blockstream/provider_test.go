package blockstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/pkg/httpclient"
)

type tipServer struct {
	*httptest.Server
	hits atomic.Int32
	path string
}

func newTipServer(t *testing.T, status int, body, path string) *tipServer {
	t.Helper()
	srv := &tipServer{path: path}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.hits.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, path, r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProvider_Reads_TheMainnetTip(t *testing.T) {
	const path = "/api/blocks/tip/height"
	srv := newTipServer(t, http.StatusOK, "850000\n", path)
	p := New()
	p.client = httpclient.Wrap(srv.Client())
	p.mainnetURL = srv.URL + path

	height, err := p.GetBlockHeight(context.Background(), models.ChainBTC)

	require.NoError(t, err)
	assert.Equal(t, uint64(850000), height)
	assert.Equal(t, int32(1), srv.hits.Load())
}

func TestProvider_Reads_TheTestnet3Tip(t *testing.T) {
	const path = "/testnet/api/blocks/tip/height"
	srv := newTipServer(t, http.StatusOK, "4800000\n", path)
	p := New()
	p.client = httpclient.Wrap(srv.Client())
	p.testnetURL = srv.URL + path

	height, err := p.GetBlockHeight(context.Background(), models.ChainTBTC)

	require.NoError(t, err)
	assert.Equal(t, uint64(4800000), height)
	assert.Equal(t, int32(1), srv.hits.Load())
}

func TestProvider_Defaults_ToBlockstream(t *testing.T) {
	p := New()
	assert.Equal(t, mainnetTipURL, p.mainnetURL)
	assert.Equal(t, testnetTipURL, p.testnetURL)
}

func TestProvider_Rejects_ChainIDsAndBadResponses(t *testing.T) {
	ctx := context.Background()
	for _, key := range []string{models.ChainETH, blockheight.TipSourceBitcoinTestnet4, ""} {
		_, err := New().GetBlockHeight(ctx, key)
		assert.Error(t, err, key)
		assert.Contains(t, err.Error(), "unknown chain_id")
	}

	const path = "/api/blocks/tip/height"
	for name, srv := range map[string]*tipServer{
		"http 429":     newTipServer(t, http.StatusTooManyRequests, "slow down", path),
		"http 500":     newTipServer(t, http.StatusInternalServerError, "boom", path),
		"not a number": newTipServer(t, http.StatusOK, "<html>", path),
		"negative":     newTipServer(t, http.StatusOK, "-1", path),
		"zero":         newTipServer(t, http.StatusOK, "0", path),
	} {
		p := New()
		p.client = httpclient.Wrap(srv.Client())
		p.mainnetURL = srv.URL + path
		_, err := p.GetBlockHeight(ctx, models.ChainBTC)
		assert.Error(t, err, name)
	}
}
