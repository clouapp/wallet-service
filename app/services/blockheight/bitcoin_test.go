package blockheight

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

// tipSource is one Bitcoin tip port. The HTTP readers live in the adapters.
type tipSource struct {
	height uint64
	err    error
	hits   atomic.Int32
	keys   []string
}

func (s *tipSource) GetBlockHeight(_ context.Context, key string) (uint64, error) {
	s.hits.Add(1)
	s.keys = append(s.keys, key)
	if s.err != nil {
		return 0, s.err
	}
	return s.height, nil
}

func bitcoinProviderAgainst(blockstream, testnet4 Provider) *BitcoinProvider {
	return NewBitcoinProvider(BitcoinDeps{Blockstream: blockstream, Testnet4: testnet4})
}

func TestBitcoin_Provider_Testnet4NeverReachesBlockstream(t *testing.T) {
	blockstream := &tipSource{height: 4800000}
	testnet4 := &tipSource{height: 154745}
	p := bitcoinProviderAgainst(blockstream, testnet4)

	height, err := p.GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)

	require.NoError(t, err)
	assert.Equal(t, uint64(154745), height)
	assert.Equal(t, int32(1), testnet4.hits.Load())
	assert.Zero(t, blockstream.hits.Load())
}

func TestBitcoin_Provider_Testnet4FailureDoesNotFallBackToTestnet3(t *testing.T) {
	blockstream := &tipSource{height: 4800000}
	testnet4 := &tipSource{err: errors.New("down")}
	p := bitcoinProviderAgainst(blockstream, testnet4)

	_, err := p.GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)

	assert.Error(t, err)
	assert.Zero(t, blockstream.hits.Load())
}

func TestBitcoin_Provider_MissingTestnet4IsAnError(t *testing.T) {
	_, err := NewBitcoinProvider(BitcoinDeps{}).GetBlockHeight(context.Background(), TipSourceBitcoinTestnet4)
	assert.Error(t, err)
}

func TestBitcoin_Provider_MissingBlockstreamIsAnError(t *testing.T) {
	_, err := NewBitcoinProvider(BitcoinDeps{}).GetBlockHeight(context.Background(), models.ChainBTC)
	assert.Error(t, err)
}

func TestBitcoin_Provider_MainnetAndTestnet3StayOnBlockstream(t *testing.T) {
	blockstream := &tipSource{height: 4800000}
	testnet4 := &tipSource{height: 154745}
	p := bitcoinProviderAgainst(blockstream, testnet4)

	for _, chainID := range []string{models.ChainBTC, models.ChainTBTC} {
		height, err := p.GetBlockHeight(context.Background(), chainID)
		require.NoError(t, err)
		assert.Equal(t, uint64(4800000), height)
	}
	assert.Equal(t, int32(2), blockstream.hits.Load())
	assert.Equal(t, []string{models.ChainBTC, models.ChainTBTC}, blockstream.keys)
	assert.Zero(t, testnet4.hits.Load())
}

func TestRouted_BitcoinProvider_ARecordOnTestnet4ReadsTheTestnet4Tip(t *testing.T) {
	blockstream := &tipSource{height: 4800000}
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
	assert.Equal(t, []string{models.ChainTBTC}, blockstream.keys)
}
