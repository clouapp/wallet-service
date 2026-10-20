package providers

import "github.com/macrowallets/waas/app/models"

// The inbound push providers, by the name the ingest route and the
// webhook_subscriptions rows use.
const (
	ProviderAlchemy   = "alchemy"
	ProviderHelius    = "helius"
	ProviderQuickNode = "quicknode"
)

// subscriptionNetworks names, per provider, the networks it pushes and the
// identifier its API takes for each: Alchemy's Notify network enum, our own Solana
// network names for Helius (its webhook type follows devnet or mainnet), and the
// QuickNode Streams network slug.
var subscriptionNetworks = map[string]map[string]string{
	ProviderAlchemy: {
		models.NetworkEthereumMainnet: "ETH_MAINNET",
		models.NetworkEthereumSepolia: "ETH_SEPOLIA",
		models.NetworkPolygonMainnet:  "MATIC_MAINNET",
		models.NetworkPolygonAmoy:     "MATIC_AMOY",
		models.NetworkBaseMainnet:     "BASE_MAINNET",
		models.NetworkBaseSepolia:     "BASE_SEPOLIA",
		models.NetworkArbitrumMainnet: "ARB_MAINNET",
		models.NetworkArbitrumSepolia: "ARB_SEPOLIA",
		models.NetworkBSCMainnet:      "BNB_MAINNET",
		models.NetworkBSCTestnet:      "BNB_TESTNET",
	},
	ProviderHelius: {
		models.NetworkSolanaMainnet: models.NetworkSolanaMainnet,
		models.NetworkSolanaDevnet:  models.NetworkSolanaDevnet,
	},
	ProviderQuickNode: {
		models.NetworkBitcoinMainnet:  "bitcoin-mainnet",
		models.NetworkBitcoinTestnet4: "bitcoin-testnet4",
	},
}

// SubscriptionNetwork returns the identifier provider's API takes for network (a
// models.Network* name) and false when the provider does not push that network.
func SubscriptionNetwork(provider, network string) (string, bool) {
	name, ok := subscriptionNetworks[provider][network]
	return name, ok
}
