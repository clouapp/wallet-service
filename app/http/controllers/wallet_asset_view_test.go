package controllers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

const (
	amoyUSDCContract    = "0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582"
	mainnetWETHContract = "0x7ceB23fD6bC0adD59E62ac25578270cFf1b9f619"
	devnetUSDCMint      = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
)

func polygonCatalog() assetDecimalsCatalog {
	return newAssetDecimalsCatalog(
		&models.Chain{ID: "polygon", NativeSymbol: "pol", NativeDecimals: 18},
		[]models.Token{{ChainID: "polygon", Symbol: "USDC", ContractAddress: amoyUSDCContract, Decimals: 6}},
	)
}

func TestTokenTransactionGetsTheTokenDecimals(t *testing.T) {
	t.Parallel()

	tx := models.Transaction{Chain: "polygon", Asset: "USDC", Amount: "3000000", TokenContract: "0x41e94eb019c0762f9bfcf9fb1e58725bfb0e7582"}

	got := polygonCatalog().decimalsFor(tx)

	if got == nil || *got != 6 {
		t.Fatalf("decimals = %v, want 6", got)
	}
}

func TestNativeTransactionGetsTheChainNativeDecimals(t *testing.T) {
	t.Parallel()

	tx := models.Transaction{Chain: "polygon", Asset: "MATIC", Amount: "1000000000000000000"}

	got := polygonCatalog().decimalsFor(tx)

	if got == nil || *got != 18 {
		t.Fatalf("decimals = %v, want 18", got)
	}
}

func TestUnknownTokenContractHasNoDecimals(t *testing.T) {
	t.Parallel()

	tx := models.Transaction{Chain: "polygon", Asset: "WETH", Amount: "1", TokenContract: mainnetWETHContract}

	if got := polygonCatalog().decimalsFor(tx); got != nil {
		t.Fatalf("decimals = %d, want none for a token the chain does not configure", *got)
	}
}

func TestSolanaMintMatchesExactly(t *testing.T) {
	t.Parallel()

	catalog := newAssetDecimalsCatalog(
		&models.Chain{ID: "tsol", NativeDecimals: 9},
		[]models.Token{{ChainID: "tsol", Symbol: "USDC", ContractAddress: devnetUSDCMint, Decimals: 6}},
	)

	exact := catalog.decimalsFor(models.Transaction{Chain: "tsol", Asset: "USDC", TokenContract: devnetUSDCMint})
	lowered := catalog.decimalsFor(models.Transaction{Chain: "tsol", Asset: "USDC", TokenContract: "4zmmc9srt5ri5x14gagxhaHii3gnpaeeryPJgZJDncDU"})

	if exact == nil || *exact != 6 {
		t.Fatalf("exact mint decimals = %v, want 6", exact)
	}
	if lowered != nil {
		t.Fatalf("case-changed mint decimals = %d, want none", *lowered)
	}
}

func TestMissingChainLeavesNativeDecimalsUnknown(t *testing.T) {
	t.Parallel()

	catalog := newAssetDecimalsCatalog(nil, nil)

	if got := catalog.decimalsFor(models.Transaction{Chain: "polygon", Asset: "MATIC", Amount: "1"}); got != nil {
		t.Fatalf("decimals = %d, want none without a chain record", *got)
	}
}

func TestTransactionViewKeepsTheTransactionFieldsAndAddsDecimals(t *testing.T) {
	t.Parallel()

	txID := uuid.New()
	views := walletTransactionViews(
		[]models.Transaction{{ID: txID, Chain: "polygon", Asset: "USDC", Amount: "3000000", TokenContract: amoyUSDCContract, TxType: models.TxTypeWithdrawal}},
		polygonCatalog(),
	)

	raw, err := json.Marshal(views[0])
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != txID.String() || body["amount"] != "3000000" || body["tx_type"] != models.TxTypeWithdrawal {
		t.Fatalf("transaction fields changed: %s", raw)
	}
	if body["decimals"] != float64(6) {
		t.Fatalf("decimals = %v, want 6 in %s", body["decimals"], raw)
	}
}

func marshalTransactionView(t *testing.T, tx models.Transaction) map[string]any {
	t.Helper()
	raw, err := json.Marshal(walletTransactionViews([]models.Transaction{tx}, polygonCatalog())[0])
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTransactionViewSerializesEveryTimestampWithAZone(t *testing.T) {
	t.Parallel()

	const (
		createdUTC   = "2026-10-01T04:03:27Z"
		confirmedUTC = "2026-10-01T04:06:28Z"
	)
	saoPaulo := time.FixedZone("UTC-3", -3*60*60)
	createdInstant, _ := time.Parse(time.RFC3339, createdUTC)
	confirmedInstant, _ := time.Parse(time.RFC3339, confirmedUTC)
	confirmedLocal := confirmedInstant.In(saoPaulo)
	tx := models.Transaction{Chain: "polygon", Asset: "USDC", Amount: "3000000", ConfirmedAt: &confirmedLocal}
	tx.CreatedAt = carbon.NewDateTime(carbon.FromStdTime(createdInstant.In(saoPaulo)))
	tx.UpdatedAt = carbon.NewDateTime(carbon.FromStdTime(confirmedInstant))

	body := marshalTransactionView(t, tx)

	if body["created_at"] != createdUTC || body["updated_at"] != confirmedUTC {
		t.Fatalf("created_at = %v, updated_at = %v, want %s and %s", body["created_at"], body["updated_at"], createdUTC, confirmedUTC)
	}
	confirmed, err := time.Parse(time.RFC3339, body["confirmed_at"].(string))
	if err != nil || !confirmed.Equal(confirmedInstant) {
		t.Fatalf("confirmed_at = %v (%v), want the instant %s", body["confirmed_at"], err, confirmedUTC)
	}
}

func TestTransactionViewWithoutTimestampsSerializesNull(t *testing.T) {
	t.Parallel()

	body := marshalTransactionView(t, models.Transaction{Chain: "polygon", Asset: "MATIC", Amount: "1"})

	if body["created_at"] != nil || body["updated_at"] != nil {
		t.Fatalf("created_at = %v, updated_at = %v, want null", body["created_at"], body["updated_at"])
	}
}

func TestBalancesKeepNativeAndConfiguredTokensOnly(t *testing.T) {
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

func TestTransactionViewDescribesTheFeeInTheNativeAsset(t *testing.T) {
	t.Parallel()

	withFee := marshalTransactionView(t, models.Transaction{
		Chain: "polygon", Asset: "USDC", Amount: "3000000", TokenContract: amoyUSDCContract,
		TxType: models.TxTypeWithdrawal, Fee: "52500000000000",
	})
	if withFee["fee"] != "52500000000000" || withFee["fee_asset"] != "POL" || withFee["fee_decimals"] != float64(18) {
		t.Fatalf("fee fields = %v %v %v, want the fee in POL with 18 decimals", withFee["fee"], withFee["fee_asset"], withFee["fee_decimals"])
	}

	withoutFee := marshalTransactionView(t, models.Transaction{Chain: "polygon", Asset: "POL", Amount: "1", TxType: models.TxTypeDeposit})
	if _, ok := withoutFee["fee_asset"]; ok {
		t.Fatalf("fee_asset present without a fee: %v", withoutFee)
	}
	if _, ok := withoutFee["fee_decimals"]; ok {
		t.Fatalf("fee_decimals present without a fee: %v", withoutFee)
	}
}
