package wallets

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
)

func TestWallet_Keeps_TheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	createdBy := other
	feeMin := 0
	feeMax := 10
	mult, err := decimal.NewFromString("1.5")
	if err != nil {
		t.Fatal(err)
	}
	usd, err := decimal.NewFromString("4.97")
	if err != nil {
		t.Fatal(err)
	}
	frozen := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	asset := "ETH"
	rawBal := "1"
	display := "1"
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
		FeeRateMin: &feeMin, FeeRateMax: &feeMax, FeeMultiplier: numeric.NewNullDecimal(mult), RequiredApprovals: 1,
		FrozenUntil: &frozen, ActivationCode: &activation,
		BalanceAsset: &asset, BalanceRaw: &rawBal, BalanceDisplay: &display, BalanceUSD: numeric.NewNullDecimal(usd),
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
			value: WalletFrom(wallet),
			want:  `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10",` + bodyFields + `}`,
		},
		{
			name:  "detail",
			value: WithNetworkFrom(&wallet, models.ResolvedNetwork{Name: "ethereum-mainnet", Testnet: false}),
			want:  `{` + bodyFields + `,"created_at":"2024-05-06T07:08:09Z","updated_at":"2024-05-06T07:08:10Z","network":"ethereum-mainnet","testnet":false}`,
		},
		{
			name:  "empty label keeps a zero usd pointer",
			value: WalletFrom(models.Wallet{Label: "", BalanceUSD: numeric.NewNullDecimal(decimal.Zero), Status: "active"}),
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

	nilRaw, err := json.Marshal(WalletPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil wallet = %s", nilRaw)
	}
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

func TestWith_Network_NamesTheNetworkOfAPolygonRecordConfiguredForAmoy(t *testing.T) {
	t.Parallel()

	walletID := uuid.New()
	view := WithNetworkFrom(
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

func TestWith_Network_DropsTheUSDValueOfATestnetWalletWithoutTouchingTheWallet(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon, BalanceUSD: usdValue(t, "4.97")}

	body := marshalToMap(t, WithNetworkFrom(wallet, amoyPolygonRecord().ResolveNetwork("")))

	if _, present := body["balance_usd"]; present {
		t.Fatalf("balance_usd = %v, want it omitted on a testnet", body["balance_usd"])
	}
	if !wallet.BalanceUSD.Valid {
		t.Fatal("the stored wallet lost its balance_usd")
	}
}

func TestWith_Network_KeepsTheUSDValueOnMainnet(t *testing.T) {
	t.Parallel()

	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon, BalanceUSD: usdValue(t, "4.97")}

	body := marshalToMap(t, WithNetworkFrom(wallet, mainnetPolygonRecord().ResolveNetwork("")))

	if body["balance_usd"] != 4.97 || body["testnet"] != false || body["network"] != models.NetworkPolygonMainnet {
		t.Fatalf("mainnet view = %v", body)
	}
}

func TestWith_Network_WritesExactUSDDigitsAsJSONNumbers(t *testing.T) {
	t.Parallel()

	const exactUSD = "1234567890123456.0123456789"
	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon, BalanceUSD: usdValue(t, exactUSD), FeeMultiplier: usdValue(t, "1.2500")}

	raw, err := json.Marshal(WithNetworkFrom(wallet, mainnetPolygonRecord().ResolveNetwork("")))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"balance_usd":` + exactUSD + `,`, `"fee_multiplier":1.25,`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("wallet JSON %s does not contain %s", raw, want)
		}
	}
}

func TestWith_Network_OmitsUnsetDecimals(t *testing.T) {
	t.Parallel()

	body := marshalToMap(t, WithNetworkFrom(&models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon}, mainnetPolygonRecord().ResolveNetwork("")))

	for _, field := range []string{"balance_usd", "fee_multiplier"} {
		if _, present := body[field]; present {
			t.Fatalf("%s = %v, want it omitted when NULL", field, body[field])
		}
	}
}

func TestWith_Network_OmitsTheNetworkWhenTheChainIsUnknown(t *testing.T) {
	t.Parallel()

	body := marshalToMap(t, WithNetworkFrom(&models.Wallet{ID: uuid.New(), Chain: models.ChainPolygon}, models.ResolvedNetwork{}))

	if _, present := body["network"]; present {
		t.Fatalf("network = %v, want it omitted without a chain record", body["network"])
	}
	if body["testnet"] != false {
		t.Fatalf("testnet = %v, want false without a chain record", body["testnet"])
	}
}

func TestWith_Network_SerializesCreatedAtWithAZone(t *testing.T) {
	t.Parallel()

	const createdUTC = "2026-09-30T12:00:00Z"
	created, _ := time.Parse(time.RFC3339, createdUTC)
	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainETH}
	wallet.CreatedAt = carbon.NewDateTime(carbon.FromStdTime(created.In(time.FixedZone("UTC-3", -3*60*60))))

	body := marshalToMap(t, WithNetworkFrom(wallet, models.ResolvedNetwork{}))

	if body["created_at"] != createdUTC {
		t.Fatalf("created_at = %v, want %s", body["created_at"], createdUTC)
	}
}
