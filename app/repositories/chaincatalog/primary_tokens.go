package chaincatalog

import "github.com/macrowallets/waas/app/models"

const (
	// sepoliaUSDTMock is the existing BRLA sandbox deployment, not issuer USDT.
	// https://brla-superuser-api.readme.io/reference/smart-contracts-addresses
	sepoliaUSDTMock = "0x7169D38820dfd117C3FA1f22a697dBA58d90BA06"
	iconWETH        = cmcIconBase + "/2396.png"
	iconWBTC        = cmcIconBase + "/3717.png"
	iconDAI         = cmcIconBase + "/4943.png"
	iconLINK        = cmcIconBase + "/1975.png"
	iconUNI         = cmcIconBase + "/7083.png"
	iconSOL         = cmcIconBase + "/5426.png"
	iconBONK        = cmcIconBase + "/23095.png"
	iconJUP         = cmcIconBase + "/29210.png"
	iconWIF         = cmcIconBase + "/28752.png"
)

// primaryNetworkTokens are the tokens of the networks eth, polygon and sol point at,
// chosen like the added records' by the network the record points at, so a "sol" on
// devnet lists the devnet USDC mint. Known issuer test deployments (see the models
// constants) include Circle's USDC, Chainlink's LINK and the
// native mint. Sepolia additionally uses the explicitly labelled BRLA USDT mock
// already present in teth; it is test data, not a Tether-issued deployment.
// Other mainnet-only assets are not exposed on Sepolia, Amoy or devnet.
var primaryNetworkTokens = map[string][]tokenSeed{
	models.NetworkEthereumMainnet: {
		{symbol: models.SymbolUSDT, name: "Tether USD", contractAddress: models.USDTContractETH, decimals: 6, iconURL: iconUSDT},
		{symbol: models.SymbolUSDC, name: "USD Coin", contractAddress: models.USDCContractETH, decimals: 6, iconURL: iconUSDC},
		{symbol: "WETH", name: "Wrapped Ether", contractAddress: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", decimals: 18, iconURL: iconWETH},
		{symbol: "WBTC", name: "Wrapped Bitcoin", contractAddress: "0x2260FAC5E5542a773Aa44fBCfeDf7C193bc2C599", decimals: 8, iconURL: iconWBTC},
		{symbol: "DAI", name: "Dai Stablecoin", contractAddress: "0x6B175474E89094C44Da98b954EedeAC495271d0F", decimals: 18, iconURL: iconDAI},
		{symbol: "LINK", name: "Chainlink", contractAddress: "0x514910771AF9Ca656af840dff83E8264EcF986CA", decimals: 18, iconURL: iconLINK},
		{symbol: "UNI", name: "Uniswap", contractAddress: "0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984", decimals: 18, iconURL: iconUNI},
	},
	models.NetworkEthereumSepolia: {
		{symbol: models.SymbolUSDT, name: "Mock USDT (Sepolia Test)", contractAddress: sepoliaUSDTMock, decimals: 6, iconURL: iconUSDT},
		{symbol: models.SymbolUSDC, name: "USD Coin (Test)", contractAddress: models.USDCContractSepolia, decimals: 6, iconURL: iconUSDC},
		{symbol: "LINK", name: "Chainlink (Test)", contractAddress: models.LINKContractSepolia, decimals: 18, iconURL: iconLINK},
	},
	models.NetworkPolygonMainnet: {
		{symbol: models.SymbolUSDT, name: "Tether USD", contractAddress: models.USDTContractPolygon, decimals: 6, iconURL: iconUSDT},
		{symbol: models.SymbolUSDC, name: "USD Coin", contractAddress: models.USDCContractPolygon, decimals: 6, iconURL: iconUSDC},
		{symbol: "WETH", name: "Wrapped Ether", contractAddress: "0x7ceB23fD6bC0adD59E62ac25578270cFf1b9f619", decimals: 18, iconURL: iconWETH},
		{symbol: "WBTC", name: "Wrapped Bitcoin", contractAddress: "0x1BFD67037B42Cf73acF2047067bd4F2C47D9BfD6", decimals: 8, iconURL: iconWBTC},
		{symbol: "DAI", name: "Dai Stablecoin", contractAddress: "0x8f3Cf7ad23Cd3CaDbD9735AFf958023239c6A063", decimals: 18, iconURL: iconDAI},
		{symbol: "LINK", name: "Chainlink", contractAddress: "0x53E0bca35eC356BD5ddDFebbD1Fc0fD03FaBad39", decimals: 18, iconURL: iconLINK},
	},
	models.NetworkPolygonAmoy: {
		{symbol: models.SymbolUSDC, name: "USD Coin (Test)", contractAddress: models.USDCContractAmoy, decimals: 6, iconURL: iconUSDC},
		{symbol: "LINK", name: "Chainlink (Test)", contractAddress: models.LINKContractAmoy, decimals: 18, iconURL: iconLINK},
	},
	models.NetworkSolanaMainnet: {
		{symbol: models.SymbolUSDC, name: "USD Coin", contractAddress: models.USDCMintSOL, decimals: 6, iconURL: iconUSDC},
		{symbol: models.SymbolUSDT, name: "Tether USD", contractAddress: models.USDTMintSOL, decimals: 6, iconURL: iconUSDT},
		{symbol: "WSOL", name: "Wrapped SOL", contractAddress: models.WrappedSOLMint, decimals: 9, iconURL: iconSOL},
		{symbol: "BONK", name: "Bonk", contractAddress: "DezXAZ8z7PnrnRJjz3wXBoRgixCa6xjnB7YaB1pPB263", decimals: 5, iconURL: iconBONK},
		{symbol: "JUP", name: "Jupiter", contractAddress: "JUPyiwrYJFskUPiHa7hkeR8VUtAeFoSYbKedZNsDvCN", decimals: 6, iconURL: iconJUP},
		{symbol: "WIF", name: "dogwifhat", contractAddress: "EKpQGSJtjMFqKZ9KQanSqYXRcF8fBopzLHYxdM65zcjm", decimals: 6, iconURL: iconWIF},
	},
	models.NetworkSolanaDevnet: {
		{symbol: models.SymbolUSDC, name: "USD Coin (Test)", contractAddress: models.USDCMintSolanaDevnet, decimals: 6, iconURL: iconUSDC},
		{symbol: "WSOL", name: "Wrapped SOL", contractAddress: models.WrappedSOLMint, decimals: 9, iconURL: iconSOL},
	},
}

// testRecordTokens are the tokens of teth, tpolygon and tsol. Those records point at a
// test network under every profile, a mainnet database holds them too, and their list
// is kept as it was seeded.
var testRecordTokens = []tokenSeed{
	{models.ChainTETH, models.SymbolUSDT, "Tether USD (Test)", sepoliaUSDTMock, 6, iconUSDT},
	{models.ChainTETH, models.SymbolUSDC, "USD Coin (Test)", models.USDCContractSepolia, 6, iconUSDC},
	{models.ChainTETH, "LINK", "Chainlink (Test)", models.LINKContractSepolia, 18, iconLINK},
	{models.ChainTPolygon, models.SymbolUSDT, "Tether USD (Test)", "0xBDE550eCd4C18B3A3C522E1298DC6B1530710B13", 6, iconUSDT},
	{models.ChainTPolygon, models.SymbolUSDC, "USD Coin (Test)", models.USDCContractAmoy, 6, iconUSDC},
	{models.ChainTSOL, models.SymbolUSDC, "USD Coin (Test)", models.USDCMintSolanaDevnet, 6, iconUSDC},
}

// primaryChainSeeds are eth, btc, polygon and sol: the records seeded before the added
// ones, whose network the profile decides.
func primaryChainSeeds() []chainSeed {
	var primaries []chainSeed
	for _, c := range chainSeedsWith(nil) {
		if !models.IsTestChainID(c.id) {
			primaries = append(primaries, c)
		}
	}
	return primaries
}
