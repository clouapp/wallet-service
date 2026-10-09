package walletview

import (
	"testing"

	"github.com/macrowallets/waas/app/models"
)

const (
	amoyUSDCContract    = "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	mainnetWETHContract = "0x7ceB23fD6bC0adD59E62ac25578270cFf1b9f619"
)

func TestBalances_Keep_NativeAndConfiguredTokensOnly(t *testing.T) {
	t.Parallel()

	amoyUSDC := amoyUSDCContract
	lowerAmoyUSDC := "0x41e94eb019c0762f9bfcf9fb1e58725bfb0e7582"
	weth := mainnetWETHContract
	rows := []models.WalletAssetBalance{
		{AssetType: "native", AssetSymbol: "matic", AmountDisplay: "4.97"},
		{AssetType: "token", AssetSymbol: "USDC", AssetContract: &lowerAmoyUSDC, AmountDisplay: "17"},
		{AssetType: "token", AssetSymbol: "WETH", AssetContract: &weth, AmountDisplay: "0"},
		{AssetType: "token", AssetSymbol: "GHOST", AmountDisplay: "0"},
	}

	got := configuredAssetBalances(rows, []models.Token{{ChainID: "polygon", Symbol: "USDC", ContractAddress: amoyUSDC, Decimals: 6}})

	if len(got) != 2 || got[0].AssetSymbol != "matic" || got[1].AssetSymbol != "USDC" {
		t.Fatalf("balances = %+v, want matic and USDC", got)
	}
}
