package accounts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/models"
)

func TestAccessTokenViewKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	until := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	full := models.AccessToken{
		ID: id, AccountID: other, CreatedBy: &id, Name: "ci", TokenHash: "super-secret",
		Permissions: `["wallets.read","webhooks.write"]`, IpCidr: "10.0.0.0/8", SpendingLimit: "{}", ValidUntil: &until,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		token models.AccessToken
		want  string
	}{
		{
			token: models.AccessToken{},
			want:  `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","account_id":"00000000-0000-0000-0000-000000000000","name":""}`,
		},
		{
			token: models.AccessToken{TokenHash: "super-secret"},
			want:  `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","account_id":"00000000-0000-0000-0000-000000000000","name":""}`,
		},
		{
			token: models.AccessToken{Name: "n"},
			want:  `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","account_id":"00000000-0000-0000-0000-000000000000","name":"n"}`,
		},
		{
			token: models.AccessToken{Permissions: "read"},
			want:  `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","account_id":"00000000-0000-0000-0000-000000000000","name":""}`,
		},
		{
			token: full,
			want:  `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","account_id":"22222222-2222-4222-8222-222222222222","created_by":"11111111-1111-4111-8111-111111111111","name":"ci","permissions":["wallets.read","webhooks.write"],"ip_cidr":"10.0.0.0/8","spending_limit":"{}","valid_until":"2024-05-06T07:08:09Z"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(newAccessTokenView(tc.token))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
		if tc.token.TokenHash != "" && strings.Contains(string(raw), tc.token.TokenHash) {
			t.Fatal("token hash is on the wire")
		}
	}

	nilRaw, err := json.Marshal(AccessTokenViewPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil token = %s", nilRaw)
	}
}

func TestAccessTokenViewsPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if AccessTokenViews(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := AccessTokenViews([]models.AccessToken{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(pagination.Response(AccessTokenViews(nil), 0, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null,"limit":20,"offset":0,"total":0}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(pagination.Response(AccessTokenViews([]models.AccessToken{}), 0, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[],"limit":20,"offset":0,"total":0}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
