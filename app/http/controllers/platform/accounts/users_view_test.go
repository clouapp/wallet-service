package accounts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

func TestPlatformAccountUserViewMatchesTheMemberListAndThePlatformUser(t *testing.T) {
	t.Parallel()

	membershipID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	accountID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	userID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	addedBy := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	deleted := time.Date(2024, 5, 6, 7, 8, 11, 0, time.UTC)
	suspended := time.Date(2026, 10, 3, 15, 4, 5, 0, time.UTC)
	reason := "operator note"
	defaultAccount := accountID
	user := &models.User{
		ID:               userID,
		Email:            "ada@example.com",
		PasswordHash:     "password-hash-marker",
		FullName:         "Ada Lovelace",
		TotpSecret:       "totp-secret-marker",
		TotpEnabled:      true,
		Status:           "active",
		DefaultAccountID: &defaultAccount,
		Preferences:      &models.UserPreferences{PreferredFiatCode: "USD"},
		SuspendedAt:      &suspended,
		SuspensionReason: &reason,
	}
	user.CreatedAt = created
	user.UpdatedAt = updated

	plain := models.AccountUser{
		ID: membershipID, AccountID: accountID, UserID: userID, Role: "owner", Status: "active",
		User: &models.User{ID: userID, Email: "ada@example.com", FullName: "Ada", Status: "active", TotpEnabled: true},
	}
	plain.CreatedAt = created
	plain.UpdatedAt = updated

	full := models.AccountUser{
		ID: membershipID, AccountID: accountID, UserID: userID, Role: "admin", Status: "suspended",
		AddedBy: &addedBy, DeletedAt: &deleted, User: user,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		member models.AccountUser
		want   string
	}{
		{
			member: plain,
			want:   `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","account_id":"22222222-2222-4222-8222-222222222222","user_id":"33333333-3333-4333-8333-333333333333","role":"owner","status":"active","user":{"id":"33333333-3333-4333-8333-333333333333","email":"ada@example.com","full_name":"Ada","status":"active","suspended_at":null,"totp_enabled":true}}`,
		},
		{
			member: full,
			want:   `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","account_id":"22222222-2222-4222-8222-222222222222","user_id":"33333333-3333-4333-8333-333333333333","role":"admin","status":"suspended","added_by":"44444444-4444-4444-8444-444444444444","deleted_at":"2024-05-06T07:08:11Z","user":{"id":"33333333-3333-4333-8333-333333333333","email":"ada@example.com","full_name":"Ada Lovelace","status":"active","suspended_at":"2026-10-03T15:04:05Z","totp_enabled":true}}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(newPlatformAccountUserView(tc.member))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
		for _, marker := range []string{
			"password-hash-marker", "totp-secret-marker", "operator note", "preferences",
			"default_account_id", "password_hash", "totp_secret", "code_hash", "sessions_revoked_at",
			"USD",
		} {
			if strings.Contains(string(raw), marker) {
				t.Fatalf("response includes %q in %s", marker, raw)
			}
		}
	}

	page, err := json.Marshal(platformAccountUserViews(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(page) != "[]" {
		t.Fatalf("empty page = %s", page)
	}
}
