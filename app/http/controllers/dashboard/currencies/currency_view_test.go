package currencies

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

func TestCurrencyViewKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	until := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	price := 1.5
	zero := 0.0
	logo := "https://example.test/logo.png"
	empty := ""
	full := models.Currency{
		ID: id, Name: "Dollar", Code: "USD", Symbol: "$", Type: models.CurrencyTypeFiat,
		Logo: &logo, Subunits: 2, CurrentPrice: 1, LastPrice: &price, PriceUpdatedAt: &until, Active: true,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		currency models.Currency
		want     string
	}{
		{
			currency: models.Currency{},
			want:     `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","name":"","code":"","symbol":"","type":"","subunits":0,"current_price":0,"active":false}`,
		},
		{
			currency: models.Currency{Logo: &empty, LastPrice: &zero},
			want:     `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","name":"","code":"","symbol":"","type":"","logo":"","subunits":0,"current_price":0,"last_price":0,"active":false}`,
		},
		{
			currency: full,
			want:     `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","name":"Dollar","code":"USD","symbol":"$","type":"fiat","logo":"https://example.test/logo.png","subunits":2,"current_price":1,"last_price":1.5,"price_updated_at":"2024-05-06T07:08:09Z","active":true}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(newCurrencyView(tc.currency))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}

	nilRaw, err := json.Marshal(CurrencyViewPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil currency = %s", nilRaw)
	}
}

func TestCurrencyViewsPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if CurrencyViews(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := CurrencyViews([]models.Currency{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(map[string]any{"data": CurrencyViews(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(map[string]any{"data": CurrencyViews([]models.Currency{})})
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[]}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
