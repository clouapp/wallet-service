package providers

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
)

func TestRegister_ActiveChains_CarriesTheRowFinalityPolicy(t *testing.T) {
	previous := openActiveChainEndpoint
	t.Cleanup(func() { openActiveChainEndpoint = previous })
	openActiveChainEndpoint = func(string) (string, error) { return "https://rpc.test/chain", nil }

	amoy := models.EVMNetworkIDPolygonAmoy
	sepolia := models.EVMNetworkIDEthereumSepolia
	rows := []models.Chain{
		{ID: models.ChainTPolygon, Name: "Polygon Amoy", AdapterType: models.AdapterTypeEVM, NativeSymbol: "pol", NativeDecimals: 18, NetworkID: &amoy, IsTestnet: true, RequiredConfirmations: 3, Finality: models.FinalityFinalizedTag, RpcURL: "sealed"},
		{ID: models.ChainTTron, Name: "TRON Nile", AdapterType: models.AdapterTypeTron, NativeSymbol: "trx", NativeDecimals: 6, IsTestnet: true, RequiredConfirmations: 19, Finality: models.FinalitySolidified, RpcURL: "sealed"},
		{ID: models.ChainTETH, Name: "Sepolia", AdapterType: models.AdapterTypeEVM, NativeSymbol: "eth", NativeDecimals: 18, NetworkID: &sepolia, IsTestnet: true, RequiredConfirmations: 12, Finality: models.FinalityConfirmations, RpcURL: "sealed"},
	}
	reg := chainpkg.NewRegistry()
	registerActiveChains(reg, rows, nil)

	for chainID, want := range map[string]string{
		models.ChainTPolygon: models.FinalityFinalizedTag,
		models.ChainTTron:    models.FinalitySolidified,
		models.ChainTETH:     models.FinalityConfirmations,
	} {
		adapter, err := reg.Chain(chainID)
		if err != nil {
			t.Fatalf("%s: %v", chainID, err)
		}
		if got := chainpkg.FinalityPolicyOf(adapter); got != want {
			t.Fatalf("%s finality %q, want %q", chainID, got, want)
		}
	}
}
