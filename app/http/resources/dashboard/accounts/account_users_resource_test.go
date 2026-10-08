package accounts_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/models"
)

func TestAccount_User_KeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	deleted := time.Date(2024, 5, 6, 7, 8, 11, 0, time.UTC)
	user := &models.User{ID: id, Email: "a@b.c", PasswordHash: "secret", FullName: "Ada", TotpSecret: "totp", TotpEnabled: true, Status: "active"}
	user.CreatedAt = created
	user.UpdatedAt = updated

	full := models.AccountUser{
		ID: id, AccountID: other, UserID: id, Role: "owner", Status: "active",
		AddedBy: &other, DeletedAt: &deleted, User: user,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		member models.AccountUser
		want   string
	}{
		{
			member: models.AccountUser{},
			want:   `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","account_id":"00000000-0000-0000-0000-000000000000","user_id":"00000000-0000-0000-0000-000000000000","role":"","status":""}`,
		},
		{
			member: full,
			want:   `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","account_id":"22222222-2222-4222-8222-222222222222","user_id":"11111111-1111-4111-8111-111111111111","role":"owner","status":"active","added_by":"22222222-2222-4222-8222-222222222222","deleted_at":"2024-05-06T07:08:11Z","user":{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","email":"a@b.c","full_name":"Ada","totp_enabled":true,"status":"active"}}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(accounts.AccountUserFrom(tc.member))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}

	nilRaw, err := json.Marshal(accounts.AccountUserPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil member = %s", nilRaw)
	}
}

func TestAccount_Users_PreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if accounts.AccountUsersFrom(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := accounts.AccountUsersFrom([]models.AccountUser{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(pagination.Response(accounts.AccountUsersFrom(nil), 0, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null,"limit":20,"offset":0,"total":0}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(pagination.Response(accounts.AccountUsersFrom([]models.AccountUser{}), 0, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[],"limit":20,"offset":0,"total":0}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
