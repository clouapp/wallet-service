package controllers

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestNetwork_ReadFromRPCURL_CoversChainsWhoseNetworkTheURLNames(t *testing.T) {
	cases := map[string]bool{
		models.AdapterTypeSolana:  true,
		models.AdapterTypeBitcoin: true,
		models.AdapterTypeEVM:     false,
		"":                        false,
	}
	for adapterType, want := range cases {
		if got := networkReadFromRPCURL(adapterType); got != want {
			t.Errorf("%q: got %v, want %v", adapterType, got, want)
		}
	}
}

func TestBitcoin_Testnet_ChainOnTestnet4URLResolvesToTestnet4(t *testing.T) {
	chainRecord := models.Chain{ID: models.ChainBTC, AdapterType: models.AdapterTypeBitcoin, IsTestnet: true}
	resolved := chainRecord.ResolveNetwork("https://mempool.space/testnet4/api")
	if resolved.Name != "bitcoin-testnet4" || !resolved.Testnet {
		t.Fatalf("resolved %+v", resolved)
	}
}
