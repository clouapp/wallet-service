package chains

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

func TestChainResourceViewKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	desc := "explorer"
	empty := ""
	full := models.ChainResource{
		ID: id, ChainID: "eth", Type: "explorer", Name: "Etherscan",
		URL: "https://etherscan.io", Description: &desc, DisplayOrder: 2, Status: "active",
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		resource models.ChainResource
		want     string
	}{
		{
			resource: models.ChainResource{},
			want:     `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","chain_id":"","type":"","name":"","url":"","display_order":0,"status":""}`,
		},
		{
			resource: models.ChainResource{Description: &empty},
			want:     `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","chain_id":"","type":"","name":"","url":"","description":"","display_order":0,"status":""}`,
		},
		{
			resource: full,
			want:     `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","chain_id":"eth","type":"explorer","name":"Etherscan","url":"https://etherscan.io","description":"explorer","display_order":2,"status":"active"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(newChainResourceView(tc.resource))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}
}

func TestChainResourceViewsPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if chainResourceViews(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := chainResourceViews([]models.ChainResource{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(map[string]any{"data": chainResourceViews(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(map[string]any{"data": chainResourceViews([]models.ChainResource{})})
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[]}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
