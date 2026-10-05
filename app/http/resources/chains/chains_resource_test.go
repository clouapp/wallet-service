package chains_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/goravel/framework/support/carbon"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/http/resources/chains"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

func TestChainKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	network := int64(1)
	zeroNetwork := int64(0)
	icon := "https://example.test/icon.png"
	empty := ""
	mainnet := "eth"
	threshold := "gas-secret-value"
	dust := "dust-secret-value"
	usd := numeric.NewNullDecimal(decimal.RequireFromString("12.34"))
	full := models.Chain{
		ID: "eth", Name: "Ethereum", AdapterType: models.AdapterTypeEVM, NativeSymbol: "eth",
		NativeDecimals: 18, NetworkID: &network, RpcURL: "rpc-secret-value", IsTestnet: false,
		MainnetChainID: &mainnet, RequiredConfirmations: 12, IconURL: &icon, DisplayOrder: 1,
		Status: "active", GasReadinessThresholdRaw: &threshold, DustThresholdNativeRaw: &dust, DustThresholdUSD: usd,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		chain models.Chain
		want  string
	}{
		{
			chain: models.Chain{},
			want:  `{"created_at":null,"updated_at":null,"id":"","name":"","adapter_type":"","native_symbol":"","native_decimals":0,"is_testnet":false,"required_confirmations":0,"display_order":0,"status":""}`,
		},
		{
			chain: models.Chain{NetworkID: &zeroNetwork, IconURL: &empty, RpcURL: "rpc-secret-value"},
			want:  `{"created_at":null,"updated_at":null,"id":"","name":"","adapter_type":"","native_symbol":"","native_decimals":0,"network_id":0,"is_testnet":false,"required_confirmations":0,"icon_url":"","display_order":0,"status":""}`,
		},
		{
			chain: full,
			want:  `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"eth","name":"Ethereum","adapter_type":"evm","native_symbol":"eth","native_decimals":18,"network_id":1,"is_testnet":false,"mainnet_chain_id":"eth","required_confirmations":12,"icon_url":"https://example.test/icon.png","display_order":1,"status":"active"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(chains.ChainFrom(tc.chain))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
		for _, secret := range []string{tc.chain.RpcURL, stringOrEmpty(tc.chain.GasReadinessThresholdRaw), stringOrEmpty(tc.chain.DustThresholdNativeRaw)} {
			if secret != "" && strings.Contains(string(raw), secret) {
				t.Fatal("a chain secret is on the wire")
			}
		}
		if tc.chain.DustThresholdUSD.Valid && strings.Contains(string(raw), "12.34") {
			t.Fatal("dust threshold USD is on the wire")
		}
	}

	nilRaw, err := json.Marshal(chains.ChainPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil chain = %s", nilRaw)
	}
}

func TestChainsFromPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if chains.ChainsFrom(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := chains.ChainsFrom([]models.Chain{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(map[string]any{"data": chains.ChainsFrom(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(map[string]any{"data": chains.ChainsFrom([]models.Chain{})})
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[]}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
