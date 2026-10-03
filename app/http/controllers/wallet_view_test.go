package controllers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
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

func floatPointer(value float64) *float64 { return &value }

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

	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon, BalanceUSD: floatPointer(4.97)}

	body := marshalToMap(t, newWalletView(wallet, amoyPolygonRecord().ResolveNetwork("")))

	if _, present := body["balance_usd"]; present {
		t.Fatalf("balance_usd = %v, want it omitted on a testnet", body["balance_usd"])
	}
	if wallet.BalanceUSD == nil {
		t.Fatal("the stored wallet lost its balance_usd")
	}
}

func TestWalletViewKeepsTheUSDValueOnMainnet(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon, BalanceUSD: floatPointer(4.97)}

	body := marshalToMap(t, newWalletView(wallet, mainnetPolygonRecord().ResolveNetwork("")))

	if body["balance_usd"] != 4.97 || body["testnet"] != false || body["network"] != models.NetworkPolygonMainnet {
		t.Fatalf("mainnet view = %v", body)
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
		{WalletID: walletID, ChainID: models.ChainPolygon, AssetType: "token", AssetSymbol: "USDC", AssetContract: &usdcContract, Decimals: 6, AmountRaw: "6000000", AmountDisplay: "6", PriceUSD: floatPointer(1), ValueUSD: floatPointer(6)},
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
	if assets[1].ValueUSD == nil {
		t.Fatal("the stored balance row lost its value_usd")
	}
	if body["testnet"] != true || body["label"] != "polygon_deposit" {
		t.Fatalf("list item = %v", body)
	}
}

func TestWalletBodyAndDetailViewsKeepTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	createdBy := other
	feeMin := 0
	feeMax := 10
	mult := 1.5
	frozen := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	asset := "ETH"
	rawBal := "1"
	display := "1"
	usd := 4.97
	zeroUSD := 0.0
	activation := "123456"
	const share = "share-secret"
	const cipher = "cipher-secret"
	const iv = "iv-secret"
	const salt = "salt-secret"

	addr := models.Address{
		ID: id, WalletID: other, Chain: "eth", Address: "0xabc",
		ExternalUserID: "system", IsActive: true, Label: "Deposit Address",
		CreatedBy: &createdBy, DerivationType: "genesis",
		EncryptedPrivateKey: cipher, EncryptionIV: iv, EncryptionSalt: salt,
	}
	addr.CreatedAt = created
	addr.UpdatedAt = updated

	wallet := models.Wallet{
		ID: id, Chain: "eth", Label: "hot",
		MPCCustomerShare: share, MPCShareIV: iv, MPCShareSalt: salt,
		MPCSecretARN: "arn-secret", MPCPublicKey: "pub-secret", MPCCurve: "secp256k1", MPCChainCode: "code-secret",
		AddressIndex: 3, DepositAddressID: &other, AccountID: &other, Status: "active",
		FeeRateMin: &feeMin, FeeRateMax: &feeMax, FeeMultiplier: &mult, RequiredApprovals: 1,
		FrozenUntil: &frozen, ActivationCode: &activation,
		BalanceAsset: &asset, BalanceRaw: &rawBal, BalanceDisplay: &display, BalanceUSD: &usd,
		BalanceLastSyncedAt: &frozen, ReadModelStatus: "idle", GasStatus: "unseeded",
		GasLastCheckedAt: &frozen, SweepPolicyVersion: 1, DepositAddress: &addr,
	}
	wallet.CreatedAt = created
	wallet.UpdatedAt = updated

	deposit := `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","chain":"eth","address":"0xabc","derivation_index":0,"external_user_id":"system","metadata":"","is_active":true,"label":"Deposit Address","created_by":"22222222-2222-4222-8222-222222222222","derivation_type":"genesis"}`
	bodyFields := `"id":"11111111-1111-4111-8111-111111111111","chain":"eth","label":"hot","address_index":3,"deposit_address_id":"22222222-2222-4222-8222-222222222222","account_id":"22222222-2222-4222-8222-222222222222","status":"active","fee_rate_min":0,"fee_rate_max":10,"fee_multiplier":1.5,"required_approvals":1,"frozen_until":"2024-05-06T07:08:09Z","balance_asset":"ETH","balance_raw":"1","balance":"1","balance_usd":4.97,"balance_last_synced_at":"2024-05-06T07:08:09Z","read_model_status":"idle","gas_status":"unseeded","gas_last_checked_at":"2024-05-06T07:08:09Z","sweep_policy_version":1,"deposit_address":` + deposit

	cases := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "body",
			value: newWalletBodyView(wallet),
			want:  `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10",` + bodyFields + `}`,
		},
		{
			name:  "detail",
			value: newWalletView(&wallet, models.ResolvedNetwork{Name: "ethereum-mainnet", Testnet: false}),
			want:  `{` + bodyFields + `,"created_at":"2024-05-06T07:08:09Z","updated_at":"2024-05-06T07:08:10Z","network":"ethereum-mainnet","testnet":false}`,
		},
		{
			name:  "list",
			value: newWalletListItem(wallet, models.ResolvedNetwork{Name: "ethereum-mainnet", Testnet: false}, nil),
			want:  `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10",` + bodyFields + `,"network":"ethereum-mainnet","testnet":false,"assets":[]}`,
		},
		{
			name:  "empty label keeps a zero usd pointer",
			value: newWalletBodyView(models.Wallet{Label: "", BalanceUSD: &zeroUSD, Status: "active"}),
			want:  `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","chain":"","address_index":0,"status":"active","required_approvals":0,"balance_usd":0,"read_model_status":"","gas_status":"","sweep_policy_version":0}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(tc.value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{share, cipher, iv, salt, activation, "arn-secret", "pub-secret", "code-secret"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("%s put key material on the wire", tc.name)
			}
		}
		if string(raw) != tc.want {
			t.Fatalf("%s wire changed\n got %s\nwant %s", tc.name, raw, tc.want)
		}
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
