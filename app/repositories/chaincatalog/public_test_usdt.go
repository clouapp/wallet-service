package chaincatalog

import "github.com/macrowallets/waas/app/models"

// These are existing public test deployments published by external providers.
// USDT is the custody API's logical asset key, not a claim that Tether issued the
// token. The provider and differing on-chain symbols stay explicit in the names
// and provenance below. No token deployment or distribution is performed here.
const (
	amoyZamaUSDT   = "0x164F5A056166d8F2ce09FdAc6d040209a8C94d01"
	devnetOrcaUSDT = "H8UekPGwePSmQ3ttuYGPU1szyFfjZR4N53rymSFwpLPm"
	arbPushUSDT    = "0xE30928528f52CAEeB75fB07837e22d77D47e9c07"
)

type publicTestToken struct {
	seed             tokenSeed
	documentedSymbol string
	provenanceURL    string
}

// This overlay is restricted to the three physical test networks. Legacy
// t-prefixed records retain their existing seed lists under every profile.
var publicTestUSDT = map[string]publicTestToken{
	models.NetworkPolygonAmoy: {
		seed:             tokenSeed{symbol: models.SymbolUSDT, name: "Public test USDT (Zama; on-chain USDTMock)", contractAddress: amoyZamaUSDT, decimals: 6, iconURL: iconUSDT},
		documentedSymbol: "USDTMock",
		provenanceURL:    "https://github.com/zama-ai/protocol-registry/blob/main/testnet.md",
	},
	models.NetworkSolanaDevnet: {
		seed:             tokenSeed{symbol: models.SymbolUSDT, name: "devUSDT (Orca public test token)", contractAddress: devnetOrcaUSDT, decimals: 6, iconURL: iconUSDT},
		documentedSymbol: "devUSDT",
		provenanceURL:    "https://github.com/orca-so/whirlpools-sdk-tutorial-legacy#devtokens",
	},
	models.NetworkArbitrumSepolia: {
		seed:             tokenSeed{symbol: models.SymbolUSDT, name: "Public test USDT (Push)", contractAddress: arbPushUSDT, decimals: 6, iconURL: iconUSDT},
		documentedSymbol: "USDT",
		provenanceURL:    "https://push.org/docs/chain/setup/smart-contract-address-book/",
	},
}

func publicTestUSDTForNetwork(network string) (tokenSeed, bool) {
	if !models.IsTestnetNetwork(network) {
		return tokenSeed{}, false
	}
	token, found := publicTestUSDT[network]
	return token.seed, found
}
