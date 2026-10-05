package mempool

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

const tipHeightPath = "/blocks/tip/height"

type tipServer struct {
	*httptest.Server
	hits atomic.Int32
}

func newTipServer(t *testing.T, status int, body string) *tipServer {
	t.Helper()
	srv := &tipServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.hits.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, tipHeightPath, r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProvider_ReadsTheTip(t *testing.T) {
	srv := newTipServer(t, http.StatusOK, "154745\n")
	p := New()
	p.client = httpclient.Wrap(srv.Client())
	p.url = srv.URL + tipHeightPath

	height, err := p.GetBlockHeight(context.Background(), blockheight.TipSourceBitcoinTestnet4)

	require.NoError(t, err)
	assert.Equal(t, uint64(154745), height)
}

func TestProvider_DefaultsToMempoolSpaceTestnet4(t *testing.T) {
	assert.Equal(t, tipHeightURL, New().url)
}

func TestProvider_RejectsChainIDsAndBadResponses(t *testing.T) {
	ctx := context.Background()
	for _, key := range []string{models.ChainTBTC, models.ChainBTC, ""} {
		_, err := New().GetBlockHeight(ctx, key)
		assert.Error(t, err, key)
	}

	for name, srv := range map[string]*tipServer{
		"http 429":     newTipServer(t, http.StatusTooManyRequests, "slow down"),
		"http 500":     newTipServer(t, http.StatusInternalServerError, "boom"),
		"not a number": newTipServer(t, http.StatusOK, "<html>"),
		"negative":     newTipServer(t, http.StatusOK, "-1"),
		"zero":         newTipServer(t, http.StatusOK, "0"),
	} {
		p := New()
		p.client = httpclient.Wrap(srv.Client())
		p.url = srv.URL + tipHeightPath
		_, err := p.GetBlockHeight(ctx, blockheight.TipSourceBitcoinTestnet4)
		assert.Error(t, err, name)
	}
}
