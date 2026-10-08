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
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","chain_id":"","asset_type":"","asset_symbol":"","asset_key":"","decimals":0,"amount_raw":"","amount_display":"","last_synced_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			row:  models.WalletAssetBalance{AssetName: &empty, PriceUSD: zero},
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","chain_id":"","asset_type":"","asset_symbol":"","asset_name":"","asset_key":"","decimals":0,"amount_raw":"","amount_display":"","price_usd":0,"last_synced_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			row:  full,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","chain_id":"eth","asset_type":"token","asset_symbol":"USDC","asset_name":"USD Coin","asset_contract":"0xabc","asset_key":"eth:usdc","decimals":6,"amount_raw":"6000000","amount_display":"6","price_usd":1.5,"value_usd":1.5,"source_address":"0xsource","last_synced_at":"2024-05-06T07:08:09Z"}`,
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
