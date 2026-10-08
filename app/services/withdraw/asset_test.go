package withdraw

import (
	"math/big"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

func TestResolve_WithdrawalAmount_USDTUses6Decimals(t *testing.T) {
	tokens := []types.Token{{
		Symbol: models.SymbolUSDT, ChainID: models.ChainETH, Decimals: 6,
		Contract: models.USDTContractETH,
	}}
	got, err := ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, models.SymbolUSDT, "1.5", tokens)
	if err != nil {
		t.Fatal(err)
	}
	if got.WalletAsset != models.SymbolUSDT || got.BaseUnits.Cmp(big.NewInt(1_500_000)) != 0 {
		t.Fatalf("%+v", got)
	}
	if got.Token == nil || got.Token.Decimals != 6 {
		t.Fatal("expected token")
	}
}

func TestResolve_WithdrawalAmount_OmittedAssetIsNative(t *testing.T) {
	got, err := ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, "", "0.001", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := new(big.Int).SetString("1000000000000000", 10)
	if got.WalletAsset != models.NativeETH || got.BaseUnits.Cmp(want) != 0 || got.Token != nil {
		t.Fatalf("%+v", got)
	}
}

func TestResolve_WithdrawalAmount_POLIsNative(t *testing.T) {
	got, err := ResolveWithdrawalAmount(models.ChainPolygon, models.NativePOL, 18, models.NativePOL, "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := new(big.Int).SetString("1000000000000000000", 10)
	if got.WalletAsset != models.NativePOL || got.BaseUnits.Cmp(want) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestResolve_WithdrawalAmount_LegacyMaticIsAnAliasOfTheNativePOL(t *testing.T) {
	want, _ := new(big.Int).SetString("2500000000000000000", 10)
	for _, requested := range []string{types.LegacyNativeSymbolMATIC, "MATIC", " Matic ", types.NativeSymbolPOL, "POL"} {
		got, err := ResolveWithdrawalAmount(models.ChainPolygon, types.NativeSymbolPOL, 18, requested, "2.5", nil)
		if err != nil {
			t.Fatalf("%q: %v", requested, err)
		}
		if got.WalletAsset != types.NativeSymbolPOL || got.Token != nil || got.BaseUnits.Cmp(want) != 0 {
			t.Fatalf("%q resolved to %+v, want the native %s", requested, got, types.NativeSymbolPOL)
		}
	}
}

func TestResolve_WithdrawalAmount_MaticIsNotAliasedOnOtherChains(t *testing.T) {
	_, err := ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, types.LegacyNativeSymbolMATIC, "1", nil)
	if err == nil {
		t.Fatal("expected MATIC to be unknown on ethereum")
	}
}

func TestResolve_WithdrawalAmount_UnknownAsset(t *testing.T) {
	_, err := ResolveWithdrawalAmount(models.ChainETH, models.NativeETH, 18, "SHIB", "1", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
