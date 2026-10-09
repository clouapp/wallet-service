package addressing

import (
	"testing"

	"github.com/btcsuite/btcd/chaincfg"

	"github.com/macrowallets/waas/app/models"
)

func TestBitcoinFamilyParams_ReturnsACopyPerChain(t *testing.T) {
	params := BitcoinFamilyParams(models.ChainLTC, false)
	if params == nil || params.PrivateKeyID != ltcMainNetPrivateKeyID {
		t.Fatalf("ltc params %+v", params)
	}
	params.PrivateKeyID = 0
	if litecoinMainNetParams.PrivateKeyID != ltcMainNetPrivateKeyID {
		t.Fatal("callers must not be able to change the adapter's parameters")
	}
	if got := BitcoinFamilyParams(models.ChainTLTC, false); got == nil || got.Bech32HRPSegwit != LtcHRPTestnet {
		t.Fatalf("tltc params %+v", got)
	}
	if got := BitcoinFamilyParams(models.ChainBTC, true); got == nil || got.PrivateKeyID != chaincfg.TestNet3Params.PrivateKeyID {
		t.Fatalf("btc testnet params %+v", got)
	}
	if BitcoinFamilyParams(models.ChainETH, false) != nil {
		t.Fatal("non Bitcoin-family chains have no UTXO parameters")
	}
}
