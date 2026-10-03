package withdrawalevents

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

type chainRows map[string]*models.Chain

func (c chainRows) FindByID(id string) (*models.Chain, error) { return c[id], nil }

func addedChainsRegistry() (*chain.Registry, chainRows) {
	registry := chain.NewRegistry()
	rows := chainRows{}
	for _, c := range []struct{ id, native string }{
		{models.ChainBase, models.NativeETH},
		{models.ChainArbitrum, models.NativeETH},
		{models.ChainBSC, models.NativeBNB},
	} {
		registry.RegisterChain(chain.NewEVMLive(chain.EVMConfig{ChainIDStr: c.id, NativeSymbol: c.native, NativeDecimal: 18}))
		rows[c.id] = &models.Chain{ID: c.id, NativeSymbol: c.native, NativeDecimals: 18}
	}
	registry.RegisterToken(types.Token{Symbol: models.SymbolUSDC, Contract: models.USDCContractBaseSepolia, Decimals: 6, ChainID: models.ChainBase})
	registry.RegisterToken(types.Token{Symbol: models.SymbolUSDC, Contract: models.USDCContractArbSepolia, Decimals: 6, ChainID: models.ChainArbitrum})
	registry.RegisterToken(types.Token{Symbol: models.SymbolUSDT, Contract: models.USDTContractBSC, Decimals: 18, ChainID: models.ChainBSC})
	registry.RegisterToken(types.Token{Symbol: models.SymbolUSDC, Contract: models.USDCContractBSC, Decimals: 18, ChainID: models.ChainBSC})
	return registry, rows
}

func TestRegistryDecimalsOfBaseArbitrumAndBSCAssets(t *testing.T) {
	registry, rows := addedChainsRegistry()
	decimals := NewRegistryDecimals(registry, rows)

	cases := []struct {
		chainID, asset string
		want           int
	}{
		{models.ChainBase, "eth", 18},
		{models.ChainBase, "ETH", 18},
		{models.ChainBase, "usdc", 6},
		{models.ChainArbitrum, "ETH", 18},
		{models.ChainArbitrum, "USDC", 6},
		{models.ChainBSC, "bnb", 18},
		{models.ChainBSC, "BNB", 18},
		{models.ChainBSC, "USDT", 18},
		{models.ChainBSC, "USDC", 18},
	}
	for _, c := range cases {
		got, ok := decimals.Decimals(c.chainID, c.asset)
		if !ok || got != c.want {
			t.Errorf("%s/%s: decimals %d ok=%t, want %d", c.chainID, c.asset, got, ok, c.want)
		}
	}
}

func TestRegistryDecimalsRefusesAssetsForeignToTheChain(t *testing.T) {
	registry, rows := addedChainsRegistry()
	decimals := NewRegistryDecimals(registry, rows)

	for _, c := range []struct{ chainID, asset string }{
		{models.ChainBSC, "ETH"},
		{models.ChainBase, "BNB"},
		{models.ChainBase, "USDT"},
		{models.ChainArbitrum, "POL"},
		{"tbsc", "BNB"},
	} {
		if got, ok := decimals.Decimals(c.chainID, c.asset); ok {
			t.Errorf("%s/%s resolved to %d decimals; it must stay unknown", c.chainID, c.asset, got)
		}
	}
}
