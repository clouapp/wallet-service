package balances_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"
	"github.com/shopspring/decimal"

	walletresource "github.com/macrowallets/waas/app/http/resources/dashboard/wallets"
	"github.com/macrowallets/waas/app/http/resources/dashboard/wallets/balances"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

func TestBalance_Keeps_TheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	name := "USD Coin"
	contract := "0xabc"
	source := "0xsource"
	empty := ""
	price := numeric.NewNullDecimal(decimal.RequireFromString("1.5"))
	zero := numeric.NewNullDecimal(decimal.Zero)
	synced := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	full := models.WalletAssetBalance{
		ID: id, WalletID: other, ChainID: "eth", AssetType: "token", AssetSymbol: "USDC",
		AssetName: &name, AssetContract: &contract, AssetKey: "eth:usdc", Decimals: 6,
		AmountRaw: "6000000", AmountDisplay: "6", PriceUSD: price, ValueUSD: price,
		SourceAddress: &source, LastSyncedAt: synced,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		row  models.WalletAssetBalance
		want string
	}{
		{
			row:  models.WalletAssetBalance{},
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","chain_id":"","asset_type":"","asset_symbol":"","asset_key":"","decimals":0,"amount_raw":"","amount_display":"","last_synced_at":"0001-01-01T00:00:00Z","unconsolidated_amount_raw":"0","unconsolidated_amount_display":"0","unconsolidated_addresses":0,"total_amount_raw":"","total_amount_display":""}`,
		},
		{
			row:  models.WalletAssetBalance{AssetName: &empty, PriceUSD: zero},
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","chain_id":"","asset_type":"","asset_symbol":"","asset_name":"","asset_key":"","decimals":0,"amount_raw":"","amount_display":"","price_usd":0,"last_synced_at":"0001-01-01T00:00:00Z","unconsolidated_amount_raw":"0","unconsolidated_amount_display":"0","unconsolidated_addresses":0,"total_amount_raw":"","total_amount_display":""}`,
		},
		{
			row:  full,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","chain_id":"eth","asset_type":"token","asset_symbol":"USDC","asset_name":"USD Coin","asset_contract":"0xabc","asset_key":"eth:usdc","decimals":6,"amount_raw":"6000000","amount_display":"6","price_usd":1.5,"value_usd":1.5,"source_address":"0xsource","last_synced_at":"2024-05-06T07:08:09Z","unconsolidated_amount_raw":"0","unconsolidated_amount_display":"0","unconsolidated_addresses":0,"total_amount_raw":"6000000","total_amount_display":"6"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(balances.BalanceFrom[struct{}](tc.row, nil))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}
}

func TestBalance_TotalAddsWhatTheOtherDepositAddressesHoldToTheBase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                       string
		row                        models.WalletAssetBalance
		raw, display, total, shown string
		addresses                  int
	}{
		{
			name: "a USDC deposit left on a child address",
			row: models.WalletAssetBalance{Decimals: 6, AmountRaw: "0", AmountDisplay: "0",
				UnconsolidatedRaw: "2200", UnconsolidatedDisplay: "0.0022", UnconsolidatedAddresses: 1},
			raw: "2200", display: "0.0022", total: "2200", shown: "0.0022", addresses: 1,
		},
		{
			name: "SOL on the base and on three children",
			row: models.WalletAssetBalance{Decimals: 9, AmountRaw: "50000000", AmountDisplay: "0.05",
				UnconsolidatedRaw: "30000000", UnconsolidatedDisplay: "0.03", UnconsolidatedAddresses: 3},
			raw: "30000000", display: "0.03", total: "80000000", shown: "0.08", addresses: 3,
		},
		{
			name: "a row stored before the refresh filled the columns",
			row:  models.WalletAssetBalance{Decimals: 9, AmountRaw: "50000000", AmountDisplay: "0.05"},
			raw:  "0", display: "0", total: "50000000", shown: "0.05",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := balances.BalanceFrom[struct{}](tc.row, nil)
			if view.UnconsolidatedAmountRaw != tc.raw || view.UnconsolidatedAmountDisplay != tc.display || view.UnconsolidatedAddresses != tc.addresses {
				t.Fatalf("unconsolidated: got %s / %s in %d addresses, want %s / %s in %d",
					view.UnconsolidatedAmountRaw, view.UnconsolidatedAmountDisplay, view.UnconsolidatedAddresses, tc.raw, tc.display, tc.addresses)
			}
			if view.TotalAmountRaw != tc.total || view.TotalAmountDisplay != tc.shown {
				t.Fatalf("total: got %s / %s, want %s / %s", view.TotalAmountRaw, view.TotalAmountDisplay, tc.total, tc.shown)
			}
			if view.AmountRaw != tc.row.AmountRaw || view.AmountDisplay != tc.row.AmountDisplay {
				t.Fatalf("amount_* must stay the base address's balance, got %s / %s", view.AmountRaw, view.AmountDisplay)
			}
		})
	}
}

func TestBalance_Keeps_ARelatedWallet(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	const share = "share-secret"
	wallet := &models.Wallet{ID: id, Chain: "eth", Label: "hot", MPCCustomerShare: share}
	raw, err := json.Marshal(balances.BalanceFrom(models.WalletAssetBalance{Wallet: wallet}, walletresource.WalletPtr(wallet)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), share) {
		t.Fatal("wallet share is on the wire")
	}
	walletRaw, err := json.Marshal(walletresource.WalletFrom(*wallet))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), string(walletRaw)) {
		t.Fatal("related wallet changed")
	}
}

func TestBalances_From_PreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if balances.BalancesFrom(nil, walletresource.WalletPtr) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := balances.BalancesFrom([]models.WalletAssetBalance{}, walletresource.WalletPtr)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(map[string]any{"data": balances.BalancesFrom(nil, walletresource.WalletPtr)})
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(map[string]any{"data": balances.BalancesFrom([]models.WalletAssetBalance{}, walletresource.WalletPtr)})
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[]}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
