package seeds

import (
	"github.com/macrowallets/waas/app/models"
)

// TRON and Litecoin records, added after the EVM ones. Like those, they are
// created by chains:add-missing on a live registry, store rpc_url as an env:NAME
// reference and take tokens, explorers and faucets from the network they point at.

const (
	iconTRX = cmcIconBase + "/1958.png"
	iconLTC = cmcIconBase + "/2.png"

	tronNativeDecimals     = 6
	litecoinNativeDecimals = 8
)

// AddedTronLitecoinChainIDs are the TRON and Litecoin records chains:add-missing may create.
var AddedTronLitecoinChainIDs = []string{
	models.ChainTron, models.ChainLTC,
	models.ChainTTron, models.ChainTLTC,
}

func addedTronLitecoinChainSeeds() []chainSeed {
	return buildAddedTronLitecoinChainSeeds(configuredConfirmations)
}

// buildAddedTronLitecoinChainSeeds lists the TRON and Litecoin records;
// confirmations returns the configured confirmations of a primary chain id (shared
// by its test record).
func buildAddedTronLitecoinChainSeeds(confirmations func(primaryChainID string) int) []chainSeed {
	tronConfirmations := confirmations(models.ChainTron)
	litecoinConfirmations := confirmations(models.ChainLTC)
	return []chainSeed{
		{id: models.ChainTron, name: "TRON", adapterType: models.AdapterTypeTron, nativeSymbol: models.NativeTRX, nativeDecimals: tronNativeDecimals, envVar: "TRON_RPC_URL", requiredConfirmations: tronConfirmations, displayOrder: 15, iconURL: iconTRX, rpcEnvReference: true},
		{id: models.ChainLTC, name: "Litecoin", adapterType: models.AdapterTypeBitcoin, nativeSymbol: models.NativeLTC, nativeDecimals: litecoinNativeDecimals, envVar: "LTC_RPC_URL", requiredConfirmations: litecoinConfirmations, displayOrder: 17, iconURL: iconLTC, rpcEnvReference: true},
		{id: models.ChainTTron, name: "TRON Nile", adapterType: models.AdapterTypeTron, nativeSymbol: models.NativeTRX, nativeDecimals: tronNativeDecimals, envVar: "TTRON_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainTron), requiredConfirmations: tronConfirmations, displayOrder: 16, iconURL: iconTRX, rpcEnvReference: true},
		{id: models.ChainTLTC, name: "Litecoin Testnet", adapterType: models.AdapterTypeBitcoin, nativeSymbol: models.NativeLTC, nativeDecimals: litecoinNativeDecimals, envVar: "TLTC_RPC_URL", isTestnet: true, mainnetChainID: strp(models.ChainLTC), requiredConfirmations: litecoinConfirmations, displayOrder: 18, iconURL: iconLTC, rpcEnvReference: true},
	}
}

// tronLitecoinTokens: USDT is the only stablecoin TRON lists (Circle no longer
// issues USDC there); the Nile USDT is the one the nileex.io faucet hands out.
// Litecoin has no tokens.
var tronLitecoinTokens = map[string][]tokenSeed{
	models.NetworkTronMainnet: {
		{symbol: models.SymbolUSDT, name: "Tether USD", contractAddress: models.USDTContractTron, decimals: 6, iconURL: iconUSDT},
	},
	models.NetworkTronNile: {
		{symbol: models.SymbolUSDT, name: "Tether USD (Test)", contractAddress: models.USDTContractTronNile, decimals: 6, iconURL: iconUSDT},
	},
}

// Tronscan (mainnet and Nile) answers with a Cloudflare check, so explorers point at
// TronQL, like the Wallets front.
var tronLitecoinResources = map[string][]resourceSeed{
	models.NetworkTronMainnet:     {{resourceType: resourceTypeExplorer, name: "TronQL Explorer", url: "https://explorer.tronql.com"}},
	models.NetworkTronNile:        {{resourceType: resourceTypeExplorer, name: "TronQL Explorer (Nile)", url: "https://explorer.tronql.com/nile"}, {resourceType: resourceTypeFaucet, name: "Nile Faucet", url: "https://nileex.io/join/getJoinPage"}},
	models.NetworkLitecoinMainnet: {{resourceType: resourceTypeExplorer, name: "Litecoin Space", url: "https://litecoinspace.org"}},
	models.NetworkLitecoinTestnet: {{resourceType: resourceTypeExplorer, name: "Litecoin Space Testnet", url: "https://litecoinspace.org/testnet"}, {resourceType: resourceTypeFaucet, name: "CypherFaucet", url: "https://cypherfaucet.com/ltc-testnet"}},
}
