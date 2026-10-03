package controllers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/macrowallets/waas/pkg/types"
)

func marshalToMap(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func amoyPolygonRecord() *models.Chain {
	amoy := models.EVMNetworkIDPolygonAmoy
	return &models.Chain{ID: models.ChainPolygon, AdapterType: models.AdapterTypeEVM, NetworkID: &amoy}
}

func mainnetPolygonRecord() *models.Chain {
	mainnet := models.EVMNetworkIDPolygonMainnet
	return &models.Chain{ID: models.ChainPolygon, AdapterType: models.AdapterTypeEVM, NetworkID: &mainnet}
}

func usdValue(t *testing.T, text string) numeric.NullDecimal {
	t.Helper()
	value, err := decimal.NewFromString(text)
	if err != nil {
		t.Fatalf("usd value %q: %v", text, err)
	}
	return numeric.NewNullDecimal(value)
}

func TestWalletViewNamesTheNetworkOfAPolygonRecordConfiguredForAmoy(t *testing.T) {
	t.Parallel()

	walletID := uuid.New()
	view := newWalletView(
		&models.Wallet{ID: walletID, Chain: models.ChainPolygon, Label: "polygon_withdraw"},
		amoyPolygonRecord().ResolveNetwork(""),
	)

	body := marshalToMap(t, view)

	if body["network"] != models.NetworkPolygonAmoy {
		t.Fatalf("network = %v, want %s", body["network"], models.NetworkPolygonAmoy)
	}
	if body["testnet"] != true {
		t.Fatalf("testnet = %v, want true for a record configured for Amoy", body["testnet"])
	}
	if body["id"] != walletID.String() || body["chain"] != models.ChainPolygon || body["label"] != "polygon_withdraw" {
		t.Fatalf("wallet fields changed: %v", body)
	}
}

func TestWalletViewDropsTheUSDValueOfATestnetWalletWithoutTouchingTheWallet(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon, BalanceUSD: usdValue(t, "4.97")}

	body := marshalToMap(t, newWalletView(wallet, amoyPolygonRecord().ResolveNetwork("")))

	if _, present := body["balance_usd"]; present {
		t.Fatalf("balance_usd = %v, want it omitted on a testnet", body["balance_usd"])
	}
	if !wallet.BalanceUSD.Valid {
		t.Fatal("the stored wallet lost its balance_usd")
	}
}

func TestWalletViewKeepsTheUSDValueOnMainnet(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon, BalanceUSD: usdValue(t, "4.97")}

	body := marshalToMap(t, newWalletView(wallet, mainnetPolygonRecord().ResolveNetwork("")))

	if body["balance_usd"] != 4.97 || body["testnet"] != false || body["network"] != models.NetworkPolygonMainnet {
		t.Fatalf("mainnet view = %v", body)
	}
}

func TestWalletViewWritesExactUSDDigitsAsJSONNumbers(t *testing.T) {
	t.Parallel()

	const exactUSD = "1234567890123456.0123456789"
	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon, BalanceUSD: usdValue(t, exactUSD), FeeMultiplier: usdValue(t, "1.2500")}

	raw, err := json.Marshal(newWalletView(wallet, mainnetPolygonRecord().ResolveNetwork("")))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"balance_usd":` + exactUSD + `,`, `"fee_multiplier":1.25,`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("wallet JSON %s does not contain %s", raw, want)
		}
	}
}

func TestWalletViewOmitsUnsetDecimals(t *testing.T) {
	t.Parallel()

	body := marshalToMap(t, newWalletView(&models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon}, mainnetPolygonRecord().ResolveNetwork("")))

	for _, field := range []string{"balance_usd", "fee_multiplier"} {
		if _, present := body[field]; present {
			t.Fatalf("%s = %v, want it omitted when NULL", field, body[field])
		}
	}
}

func TestWalletViewOmitsTheNetworkWhenTheChainIsUnknown(t *testing.T) {
	t.Parallel()

	body := marshalToMap(t, newWalletView(&models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon}, models.ResolvedNetwork{}))

	if _, present := body["network"]; present {
		t.Fatalf("network = %v, want it omitted without a chain record", body["network"])
	}
	if body["testnet"] != false {
		t.Fatalf("testnet = %v, want false without a chain record", body["testnet"])
	}
}

func TestWalletViewSerializesCreatedAtWithAZone(t *testing.T) {
	t.Parallel()

	const createdUTC = "2026-09-30T12:00:00Z"
	created, _ := time.Parse(time.RFC3339, createdUTC)
	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainETH}
	wallet.CreatedAt = carbon.NewDateTime(carbon.FromStdTime(created.In(time.FixedZone("UTC-3", -3*60*60))))

	body := marshalToMap(t, newWalletView(wallet, models.ResolvedNetwork{}))

	if body["created_at"] != createdUTC {
		t.Fatalf("created_at = %v, want %s", body["created_at"], createdUTC)
	}
}

func TestWalletListItemCarriesTokenBalancesUnpricedOnATestnet(t *testing.T) {
	t.Parallel()

	walletID := uuid.New()
	usdcContract := "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	assets := []models.WalletAssetBalance{
		{WalletID: walletID, ChainID: models.ChainPolygon, AssetType: "native", AssetSymbol: types.NativeSymbolPOL, Decimals: 18, AmountRaw: "0", AmountDisplay: "0"},
		{WalletID: walletID, ChainID: models.ChainPolygon, AssetType: "token", AssetSymbol: "USDC", AssetContract: &usdcContract, Decimals: 6, AmountRaw: "6000000", AmountDisplay: "6", PriceUSD: usdValue(t, "1"), ValueUSD: usdValue(t, "6")},
	}

	item := newWalletListItem(models.Wallet{ID: walletID, Chain: models.ChainPolygon, Label: "polygon_deposit"}, amoyPolygonRecord().ResolveNetwork(""), assets)
	body := marshalToMap(t, item)

	listed, ok := body["assets"].([]any)
	if !ok || len(listed) != len(assets) {
		t.Fatalf("assets = %v, want the %d balances", body["assets"], len(assets))
	}
	usdc := listed[1].(map[string]any)
	if usdc["asset_symbol"] != "USDC" || usdc["amount_display"] != "6" {
		t.Fatalf("usdc = %v", usdc)
	}
	if _, priced := usdc["value_usd"]; priced {
		t.Fatalf("value_usd = %v, want it omitted on a testnet", usdc["value_usd"])
	}
	if !assets[1].ValueUSD.Valid {
		t.Fatal("the stored balance row lost its value_usd")
	}
	if body["testnet"] != true || body["label"] != "polygon_deposit" {
		t.Fatalf("list item = %v", body)
	}
}

func TestWalletListItemListsNoAssetsAsAnEmptyArray(t *testing.T) {
	t.Parallel()

	body := marshalToMap(t, newWalletListItem(models.Wallet{ID: uuid.New(), Chain: models.ChainBTC}, models.ResolvedNetwork{Name: models.NetworkBitcoinMainnet}, nil))

	listed, ok := body["assets"].([]any)
	if !ok || len(listed) != 0 {
		t.Fatalf("assets = %v, want []", body["assets"])
	}
}
