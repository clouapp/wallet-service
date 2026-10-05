package chains_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/resources/dashboard/chains"
	"github.com/macrowallets/waas/app/models"
)

func TestTokenKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	icon := "https://example.test/icon.png"
	empty := ""
	full := models.Token{
		ID: id, ChainID: "eth", Symbol: "USDC", Name: "USD Coin",
		ContractAddress: "0xabc", Decimals: 6, IconURL: &icon, Status: "active",
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		token models.Token
		want  string
	}{
		{
			token: models.Token{},
			want:  `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","chain_id":"","symbol":"","name":"","contract_address":"","decimals":0,"status":""}`,
		},
		{
			token: models.Token{IconURL: &empty},
			want:  `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","chain_id":"","symbol":"","name":"","contract_address":"","decimals":0,"icon_url":"","status":""}`,
		},
		{
			token: full,
			want:  `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","chain_id":"eth","symbol":"USDC","name":"USD Coin","contract_address":"0xabc","decimals":6,"icon_url":"https://example.test/icon.png","status":"active"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(chains.TokenFrom(tc.token))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}
}

func TestTokensPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if chains.TokensFrom(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := chains.TokensFrom([]models.Token{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(map[string]any{"chain": "eth", "data": chains.TokensFrom(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"chain":"eth","data":null}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(map[string]any{"data": chains.TokensFrom([]models.Token{})})
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[]}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
