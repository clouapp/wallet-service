package chaincatalog

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
)

func TestRetiredAmoyUSDTRequiresExactChainNetworkContractAndSymbol(t *testing.T) {
	for _, tc := range []struct {
		name, chain, network, symbol, contract, status string
		retire                                         bool
	}{
		{"canonical Amoy", "polygon", models.NetworkPolygonAmoy, "USDT", retiredAmoyUSDTContract, "active", true},
		{"alias Amoy", "tpolygon", models.NetworkPolygonAmoy, "USDT", retiredAmoyUSDTContract, "active", true},
		{"mainnet untouched", "polygon", models.NetworkPolygonMainnet, "USDT", retiredAmoyUSDTContract, "active", false},
		{"other chain untouched", "eth", models.NetworkPolygonAmoy, "USDT", retiredAmoyUSDTContract, "active", false},
		{"other symbol untouched", "polygon", models.NetworkPolygonAmoy, "CUSTOM", retiredAmoyUSDTContract, "active", false},
		{"other contract untouched", "polygon", models.NetworkPolygonAmoy, "USDT", models.USDTContractPolygon, "active", false},
		{"already inactive", "polygon", models.NetworkPolygonAmoy, "USDT", retiredAmoyUSDTContract, "inactive", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := models.Token{ChainID: tc.chain, Symbol: tc.symbol, ContractAddress: tc.contract, Status: tc.status}
			require.Equal(t, tc.retire, retiredAmoyUSDT(tc.chain, tc.network, row))
		})
	}
}
func TestFreshCatalogDoesNotAdvertiseNonexistentAmoyUSDT(t *testing.T) {
	for _, token := range testRecordTokens {
		require.False(t, token.chainID == models.ChainTPolygon && token.symbol == models.SymbolUSDT && sameContract(token.contractAddress, retiredAmoyUSDTContract))
	}
	for _, token := range tokensOfNetwork(models.NetworkPolygonAmoy) {
		require.False(t, token.symbol == models.SymbolUSDT && sameContract(token.contractAddress, retiredAmoyUSDTContract))
	}
}

func TestTokenRetirementBindsExactPlannedRowAndPreservesOtherSymbols(t *testing.T) {
	rows := []models.Token{{ChainID: "polygon", Symbol: "CUSTOM", ContractAddress: retiredAmoyUSDTContract, Name: "unrelated symbol"}, {ChainID: "polygon", Symbol: "USDT", ContractAddress: retiredAmoyUSDTContract, Name: "planned checksum row"}, {ChainID: "polygon", Symbol: "USDT", ContractAddress: "0xbde550ecd4c18b3a3c522e1298dc6b1530710b13", Name: "planned lowercase row"}}
	first := findTokenChangeRow(rows, TokenChange{ChainID: "polygon", Symbol: "USDT", Contract: retiredAmoyUSDTContract})
	require.NotNil(t, first)
	require.Equal(t, "planned checksum row", first.Name)
	second := findTokenChangeRow(rows, TokenChange{ChainID: "polygon", Symbol: "USDT", Contract: "0xbde550ecd4c18b3a3c522e1298dc6b1530710b13"})
	require.NotNil(t, second)
	require.Equal(t, "planned lowercase row", second.Name)
}
