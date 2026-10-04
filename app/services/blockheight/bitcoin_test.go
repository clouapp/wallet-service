package blockheight

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
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

// bitcoinProviderAgainst points every Bitcoin tip source at a fake server.
func bitcoinProviderAgainst(blockstream, testnet4 *tipServer) *BitcoinProvider {
	p := NewBitcoinProvider()
	p.blockstream.client = httpclient.Wrap(blockstream.Client())
	p.blockstream.mainnetURL = blockstream.URL + tipHeightPath
	p.blockstream.testnetURL = blockstream.URL + tipHeightPath
	p.testnet4.client = httpclient.Wrap(testnet4.Client())
	p.testnet4.url = testnet4.URL + tipHeightPath
	return p
}

func TestMempoolTestnet4Provider_ReadsTheTip(t *testing.T) {
	srv := newTipServer(t, http.StatusOK, "154745\n")
	p := NewMempoolTestnet4Provider()
	p.client = httpclient.Wrap(srv.Client())
	p.url = srv.URL + tipHeightPath

	height, err := p.GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)

	require.NoError(t, err)
	assert.Equal(t, uint64(154745), height)
}

func TestMempoolTestnet4Provider_DefaultsToMempoolSpaceTestnet4(t *testing.T) {
	assert.Equal(t, "https://mempool.space/testnet4/api/blocks/tip/height", NewMempoolTestnet4Provider().url)
}

func TestMempoolTestnet4Provider_RejectsChainIDsAndBadResponses(t *testing.T) {
	ctx := context.Background()
	for _, key := range []string{models.ChainTBTC, models.ChainBTC, ""} {
		_, err := NewMempoolTestnet4Provider().GetBlockHeight(ctx, key)
		assert.Error(t, err, key)
	}

	for name, srv := range map[string]*tipServer{
		"http 429":     newTipServer(t, http.StatusTooManyRequests, "slow down"),
		"http 500":     newTipServer(t, http.StatusInternalServerError, "boom"),
		"not a number": newTipServer(t, http.StatusOK, "<html>"),
		"negative":     newTipServer(t, http.StatusOK, "-1"),
		"zero":         newTipServer(t, http.StatusOK, "0"),
	} {
		p := NewMempoolTestnet4Provider()
		p.client = httpclient.Wrap(srv.Client())
		p.url = srv.URL + tipHeightPath
		_, err := p.GetBlockHeight(ctx, TipSourceBitcoinTestnet4)
		assert.Error(t, err, name)
	}
}

func TestBitcoinProvider_Testnet4NeverReachesBlockstream(t *testing.T) {
	blockstream := newTipServer(t, http.StatusOK, "4800000")
	testnet4 := newTipServer(t, http.StatusOK, "154745")
	p := bitcoinProviderAgainst(blockstream, testnet4)

	height, err := p.GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)

	require.NoError(t, err)
	assert.Equal(t, uint64(154745), height)
	assert.Zero(t, blockstream.hits.Load())
}

func TestBitcoinProvider_Testnet4FailureDoesNotFallBackToTestnet3(t *testing.T) {
	blockstream := newTipServer(t, http.StatusOK, "4800000")
	testnet4 := newTipServer(t, http.StatusServiceUnavailable, "down")
	p := bitcoinProviderAgainst(blockstream, testnet4)

	_, err := p.GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)

	require.Error(t, err)
	assert.Zero(t, blockstream.hits.Load())
}

func TestBitcoinProvider_MainnetAndTestnet3StayOnBlockstream(t *testing.T) {
	blockstream := newTipServer(t, http.StatusOK, "4800000")
	testnet4 := newTipServer(t, http.StatusOK, "154745")
	p := bitcoinProviderAgainst(blockstream, testnet4)

	for _, chainID := range []string{models.ChainBTC, models.ChainTBTC} {
		height, err := p.GetBlockHeight(context.Background(), chainID)
		require.NoError(t, err)
		assert.Equal(t, uint64(4800000), height)
	}
	assert.Equal(t, int32(2), blockstream.hits.Load())
	assert.Zero(t, testnet4.hits.Load())
}

func TestRoutedBitcoinProvider_ARecordOnTestnet4ReadsTheTestnet4Tip(t *testing.T) {
	blockstream := newTipServer(t, http.StatusOK, "4800000")
	testnet4 := newTipServer(t, http.StatusOK, "154745")
	routed := RouteByNetwork(bitcoinProviderAgainst(blockstream, testnet4), map[string]string{
		models.ChainBTC:  models.NetworkBitcoinTestnet4,
		models.ChainTBTC: models.NetworkBitcoinTestnet,
	})
	ctx := context.Background()

	height, err := routed.GetBlockHeight(ctx, models.ChainBTC)
	require.NoError(t, err)
	assert.Equal(t, uint64(154745), height)
	assert.Zero(t, blockstream.hits.Load())

	height, err = routed.GetBlockHeight(ctx, models.ChainTBTC)
	require.NoError(t, err)
	assert.Equal(t, uint64(4800000), height)
}
