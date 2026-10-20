package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/app/models"
)

func TestSubscription_Network_NamesEachNetworkAProviderServes(t *testing.T) {
	cases := []struct {
		provider, network, want string
	}{
		{ProviderAlchemy, models.NetworkEthereumMainnet, "ETH_MAINNET"},
		{ProviderAlchemy, models.NetworkEthereumSepolia, "ETH_SEPOLIA"},
		{ProviderAlchemy, models.NetworkPolygonMainnet, "MATIC_MAINNET"},
		{ProviderAlchemy, models.NetworkPolygonAmoy, "MATIC_AMOY"},
		{ProviderAlchemy, models.NetworkBaseMainnet, "BASE_MAINNET"},
		{ProviderAlchemy, models.NetworkBaseSepolia, "BASE_SEPOLIA"},
		{ProviderAlchemy, models.NetworkArbitrumMainnet, "ARB_MAINNET"},
		{ProviderAlchemy, models.NetworkArbitrumSepolia, "ARB_SEPOLIA"},
		{ProviderAlchemy, models.NetworkBSCMainnet, "BNB_MAINNET"},
		{ProviderAlchemy, models.NetworkBSCTestnet, "BNB_TESTNET"},
		{ProviderHelius, models.NetworkSolanaMainnet, models.NetworkSolanaMainnet},
		{ProviderHelius, models.NetworkSolanaDevnet, models.NetworkSolanaDevnet},
		{ProviderQuickNode, models.NetworkBitcoinMainnet, "bitcoin-mainnet"},
		{ProviderQuickNode, models.NetworkBitcoinTestnet4, "bitcoin-testnet4"},
	}
	for _, tc := range cases {
		got, ok := SubscriptionNetwork(tc.provider, tc.network)
		assert.True(t, ok, "%s %s", tc.provider, tc.network)
		assert.Equal(t, tc.want, got, "%s %s", tc.provider, tc.network)
	}
}

func TestSubscription_Network_RefusesWhatAProviderDoesNotServe(t *testing.T) {
	for _, tc := range []struct{ provider, network string }{
		{ProviderAlchemy, models.NetworkSolanaMainnet},
		{ProviderAlchemy, models.NetworkTronMainnet},
		{ProviderHelius, models.NetworkSolanaTestnet},
		{ProviderHelius, models.NetworkEthereumMainnet},
		{ProviderQuickNode, models.NetworkBitcoinTestnet},
		{ProviderQuickNode, models.NetworkLitecoinMainnet},
		{"etherscan", models.NetworkEthereumMainnet},
		{ProviderAlchemy, ""},
	} {
		_, ok := SubscriptionNetwork(tc.provider, tc.network)
		assert.False(t, ok, "%s %s", tc.provider, tc.network)
	}
}
