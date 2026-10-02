package blockheight

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func TestNewProviders_WithoutAnEtherscanKeyLeavesEVMOnItsRPC(t *testing.T) {
	for _, key := range []string{"", "   ", "\t\n"} {
		providers := NewProviders(key, map[string]string{})

		_, hasEVM := providers[models.AdapterTypeEVM]
		assert.False(t, hasEVM, "key %q must not build an Etherscan provider", key)
		assert.NotNil(t, providers[models.AdapterTypeBitcoin])
		assert.NotNil(t, providers[models.AdapterTypeSolana])
	}
}

func TestNewProviders_WithAnEtherscanKeyRoutesEVMThroughEtherscan(t *testing.T) {
	providers := NewProviders(" secret-key ", map[string]string{models.ChainETH: models.NetworkEthereumSepolia})

	routed, ok := providers[models.AdapterTypeEVM].(*NetworkRouted)
	require.True(t, ok)
	etherscan, ok := routed.inner.(*EtherscanProvider)
	require.True(t, ok)
	assert.Equal(t, "secret-key", etherscan.apiKey)
	assert.Equal(t, models.NetworkEthereumSepolia, routed.networkByChain[models.ChainETH])
}

func TestNewProviders_BitcoinUsesTheTestnet4AwareProvider(t *testing.T) {
	providers := NewProviders("", map[string]string{models.ChainBTC: models.NetworkBitcoinTestnet4})

	routed, ok := providers[models.AdapterTypeBitcoin].(*NetworkRouted)
	require.True(t, ok)
	_, ok = routed.inner.(*BitcoinProvider)
	assert.True(t, ok)
	assert.Equal(t, TipSourceBitcoinTestnet4, providerChainIDByNetwork[models.NetworkBitcoinTestnet4])
}
