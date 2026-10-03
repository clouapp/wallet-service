package wallets

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

func TestWalletUserViewKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	deleted := time.Date(2024, 5, 6, 7, 8, 11, 0, time.UTC)
	user := &models.User{ID: id, Email: "a@b.c", PasswordHash: "secret", FullName: "Ada", TotpSecret: "totp", TotpEnabled: true, Status: "active"}
	user.CreatedAt = created
	user.UpdatedAt = updated

	full := models.WalletUser{
		ID: id, WalletID: other, UserID: id, Roles: "viewer", Status: "active",
		DeletedAt: &deleted, User: user,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		member models.WalletUser
		want   string
	}{
		{
			member: models.WalletUser{},
			want:   `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","user_id":"00000000-0000-0000-0000-000000000000","status":""}`,
		},
		{
			member: models.WalletUser{Roles: ""},
			want:   `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","user_id":"00000000-0000-0000-0000-000000000000","status":""}`,
		},
		{
			member: full,
			want:   `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","user_id":"11111111-1111-4111-8111-111111111111","roles":"viewer","status":"active","deleted_at":"2024-05-06T07:08:11Z","user":{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","email":"a@b.c","full_name":"Ada","totp_enabled":true,"status":"active"}}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(newWalletUserView(tc.member))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}

	nilRaw, err := json.Marshal(walletUserViewPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil member = %s", nilRaw)
	}
}

func TestWalletUserViewsPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if walletUserViews(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := walletUserViews([]models.WalletUser{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(map[string]any{"data": walletUserViews(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(map[string]any{"data": walletUserViews([]models.WalletUser{})})
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[]}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
