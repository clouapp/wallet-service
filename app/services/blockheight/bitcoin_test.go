package blockheight

import (
	"context"
	"errors"
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

// tipSource is the testnet4 port. The HTTP reader lives in the mempool adapter.
type tipSource struct {
	height uint64
	err    error
	hits   atomic.Int32
}

func (s *tipSource) GetBlockHeight(context.Context, string) (uint64, error) {
	s.hits.Add(1)
	if s.err != nil {
		return 0, s.err
	}
	return s.height, nil
}

// bitcoinProviderAgainst points Blockstream at a fake server and testnet4 at source.
func bitcoinProviderAgainst(blockstream *tipServer, testnet4 Provider) *BitcoinProvider {
	p := NewBitcoinProvider(BitcoinDeps{Testnet4: testnet4})
	p.blockstream.client = httpclient.Wrap(blockstream.Client())
	p.blockstream.mainnetURL = blockstream.URL + tipHeightPath
	p.blockstream.testnetURL = blockstream.URL + tipHeightPath
	return p
}

func TestBitcoinProvider_Testnet4NeverReachesBlockstream(t *testing.T) {
	blockstream := newTipServer(t, http.StatusOK, "4800000")
	testnet4 := &tipSource{height: 154745}
	p := bitcoinProviderAgainst(blockstream, testnet4)

	height, err := p.GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)

	require.NoError(t, err)
	assert.Equal(t, uint64(154745), height)
	assert.Equal(t, int32(1), testnet4.hits.Load())
	assert.Zero(t, blockstream.hits.Load())
}

func TestBitcoinProvider_Testnet4FailureDoesNotFallBackToTestnet3(t *testing.T) {
	blockstream := newTipServer(t, http.StatusOK, "4800000")
	testnet4 := &tipSource{err: errors.New("down")}
	p := bitcoinProviderAgainst(blockstream, testnet4)

	_, err := p.GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)

	require.Error(t, err)
	assert.Zero(t, blockstream.hits.Load())
}

func TestBitcoinProvider_MissingTestnet4IsAnError(t *testing.T) {
	_, err := NewBitcoinProvider(BitcoinDeps{}).GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)
	require.Error(t, err)
}

func TestBitcoinProvider_MainnetAndTestnet3StayOnBlockstream(t *testing.T) {
	blockstream := newTipServer(t, http.StatusOK, "4800000")
	testnet4 := &tipSource{height: 154745}
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
	testnet4 := &tipSource{height: 154745}
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
