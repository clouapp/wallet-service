package chaincatalog

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func primaryTokensByChain(t *testing.T, profile string) map[string]map[string]tokenSeed {
	t.Helper()
	tokens, err := tokensForChains(primaryChainSeeds(), profile)
	if err != nil {
		t.Fatal(err)
	}
	byChain := map[string]map[string]tokenSeed{}
	for _, token := range tokens {
		if byChain[token.chainID] == nil {
			byChain[token.chainID] = map[string]tokenSeed{}
		}
		byChain[token.chainID][token.symbol] = token
	}
	return byChain
}

func TestPrimary_Records_OnTheTestnetProfileListOnlyIssuerTestnetDeployments(t *testing.T) {
	t.Parallel()

	byChain := primaryTokensByChain(t, models.ChainNetworkProfileTestnet)
	want := map[string]map[string]struct {
		contract string
		decimals int
	}{
		models.ChainETH: {
			models.SymbolUSDC: {"0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238", 6},
			"LINK":            {"0x779877A7B0D9E8603169DdbD7836e478b4624789", 18},
		},
		models.ChainPolygon: {
			models.SymbolUSDC: {"0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582", 6},
			"LINK":            {"0x0Fd9e8d3aF1aaee056EB9e802c3A762a667b1904", 18},
		},
		models.ChainSOL: {
			models.SymbolUSDC: {"4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU", 6},
			"WSOL":            {"So11111111111111111111111111111111111111112", 9},
		},
	}
	for chainID, symbols := range want {
		got := byChain[chainID]
		if len(got) != len(symbols) {
			t.Errorf("%s: tokens %+v, want only %v", chainID, got, symbols)
			continue
		}
		for symbol, w := range symbols {
			if got[symbol].contractAddress != w.contract || got[symbol].decimals != w.decimals {
				t.Errorf("%s %s: %+v, want %s with %d decimals", chainID, symbol, got[symbol], w.contract, w.decimals)
			}
		}
	}
	if len(byChain[models.ChainBTC]) != 0 {
		t.Errorf("btc has no tokens, got %+v", byChain[models.ChainBTC])
	}
}

func TestPrimary_Records_OnTheMainnetProfileKeepTheMainnetContracts(t *testing.T) {
	t.Parallel()

	byChain := primaryTokensByChain(t, models.ChainNetworkProfileMainnet)
	if len(byChain[models.ChainETH]) != 7 || byChain[models.ChainETH][models.SymbolUSDT].contractAddress != models.USDTContractETH ||
		byChain[models.ChainETH][models.SymbolUSDC].contractAddress != models.USDCContractETH {
		t.Errorf("eth mainnet tokens %+v", byChain[models.ChainETH])
	}
	if len(byChain[models.ChainPolygon]) != 6 || byChain[models.ChainPolygon][models.SymbolUSDC].contractAddress != models.USDCContractPolygon {
		t.Errorf("polygon mainnet tokens %+v", byChain[models.ChainPolygon])
	}
	if len(byChain[models.ChainSOL]) != 6 || byChain[models.ChainSOL][models.SymbolUSDC].contractAddress != models.USDCMintSOL ||
		byChain[models.ChainSOL]["BONK"].decimals != 5 {
		t.Errorf("sol mainnet tokens %+v", byChain[models.ChainSOL])
	}
}
