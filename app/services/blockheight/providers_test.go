package blockheight

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func TestNewProviders_WithoutAKeyFunctionLeavesEVMOnItsRPC(t *testing.T) {
	for _, deps := range []ProvidersDeps{{}, {Etherscan: &tipSource{height: 1}}} {
		providers := NewProviders(deps)

		_, hasEVM := providers[models.AdapterTypeEVM]
		assert.False(t, hasEVM)
		assert.NotNil(t, providers[models.AdapterTypeBitcoin])
		assert.NotNil(t, providers[models.AdapterTypeSolana])
	}
}

func TestNewProviders_ForwardsTheEtherscanPort(t *testing.T) {
	var calls int
	etherscan := &tipSource{height: 0x10}
	providers := NewProviders(ProvidersDeps{
		Key: func(context.Context) string {
			calls++
			return "present"
		},
		Etherscan:      etherscan,
		NetworkByChain: map[string]string{models.ChainETH: models.NetworkEthereumSepolia},
	})
	if calls != 0 {
		t.Fatal("the etherscan key was read when the providers were built")
	}

	height, err := providers[models.AdapterTypeEVM].GetBlockHeight(context.Background(), models.ChainETH)

	require.NoError(t, err)
	assert.Equal(t, uint64(0x10), height)
	assert.Equal(t, int32(1), etherscan.hits.Load())
	assert.Equal(t, []string{models.ChainTETH}, etherscan.keys)
	if calls != 0 {
		t.Fatal("the service read the etherscan key on a height read")
	}
}

func TestNewProviders_ForwardsTheBlockstreamPort(t *testing.T) {
	blockstream := &tipSource{height: 850000}
	providers := NewProviders(ProvidersDeps{Blockstream: blockstream})

	height, err := providers[models.AdapterTypeBitcoin].GetBlockHeight(context.Background(), models.ChainBTC)

	require.NoError(t, err)
	assert.Equal(t, uint64(850000), height)
	assert.Equal(t, int32(1), blockstream.hits.Load())
	assert.Equal(t, []string{models.ChainBTC}, blockstream.keys)
}

func TestNewProviders_ForwardsTheSolanaPort(t *testing.T) {
	solana := &tipSource{height: 123456789}
	providers := NewProviders(ProvidersDeps{Solana: solana})

	height, err := providers[models.AdapterTypeSolana].GetBlockHeight(context.Background(), models.ChainSOL)

	require.NoError(t, err)
	assert.Equal(t, uint64(123456789), height)
	assert.Equal(t, int32(1), solana.hits.Load())
	assert.Equal(t, []string{models.ChainSOL}, solana.keys)
}

func TestNewProviders_BitcoinUsesTheTestnet4AwareProvider(t *testing.T) {
	providers := NewProviders(ProvidersDeps{NetworkByChain: map[string]string{models.ChainBTC: models.NetworkBitcoinTestnet4}})

	routed, ok := providers[models.AdapterTypeBitcoin].(*NetworkRouted)
	require.True(t, ok)
	_, ok = routed.inner.(*BitcoinProvider)
	assert.True(t, ok)
	assert.Equal(t, TipSourceBitcoinTestnet4, providerChainIDByNetwork[models.NetworkBitcoinTestnet4])
}
