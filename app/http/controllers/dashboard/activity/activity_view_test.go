package activity

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/models"
)

func TestAccountActivityViewKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	when := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)

	cases := []struct {
		row  models.AccountActivity
		want string
	}{
		{
			row:  models.AccountActivity{},
			want: `{"id":"00000000-0000-0000-0000-000000000000","account_id":null,"actor_user_id":"00000000-0000-0000-0000-000000000000","action":"","target_type":"","target_id":"","metadata":{},"created_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			row:  models.AccountActivity{Metadata: models.ActivityMetadata{}},
			want: `{"id":"00000000-0000-0000-0000-000000000000","account_id":null,"actor_user_id":"00000000-0000-0000-0000-000000000000","action":"","target_type":"","target_id":"","metadata":{},"created_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			row:  models.AccountActivity{Metadata: models.ActivityMetadata{"fields": []string(nil)}},
			want: `{"id":"00000000-0000-0000-0000-000000000000","account_id":null,"actor_user_id":"00000000-0000-0000-0000-000000000000","action":"","target_type":"","target_id":"","metadata":{"fields":null},"created_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			row:  models.AccountActivity{Metadata: models.ActivityMetadata{"fields": []string{}}},
			want: `{"id":"00000000-0000-0000-0000-000000000000","account_id":null,"actor_user_id":"00000000-0000-0000-0000-000000000000","action":"","target_type":"","target_id":"","metadata":{"fields":[]},"created_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			row: models.AccountActivity{
				ID: id, AccountID: &other, ActorUserID: id, Action: "member.role",
				TargetType: "account_user", TargetID: other.String(),
				Metadata:  models.ActivityMetadata{"role": "admin", "enabled": true},
				CreatedAt: when,
			},
			want: `{"id":"11111111-1111-4111-8111-111111111111","account_id":"22222222-2222-4222-8222-222222222222","actor_user_id":"11111111-1111-4111-8111-111111111111","action":"member.role","target_type":"account_user","target_id":"22222222-2222-4222-8222-222222222222","metadata":{"enabled":true,"role":"admin"},"created_at":"2024-05-06T07:08:09Z"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(newAccountActivityView(tc.row))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}
}

func TestAccountActivityViewsPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if accountActivityViews(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := accountActivityViews([]models.AccountActivity{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(pagination.Response(accountActivityViews(nil), 0, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null,"limit":20,"offset":0,"total":0}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(pagination.Response(accountActivityViews([]models.AccountActivity{}), 0, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[],"limit":20,"offset":0,"total":0}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
