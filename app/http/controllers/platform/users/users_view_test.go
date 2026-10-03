package users

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestPlatformUserViewOmitsSecretFields(t *testing.T) {
	t.Parallel()

	suspended := time.Date(2026, 10, 3, 15, 4, 5, 0, time.UTC)
	reason := "operator note"
	user := models.User{
		ID:                uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		Email:             "ada@example.com",
		PasswordHash:      "password-hash-marker",
		FullName:          "Ada Lovelace",
		TotpSecret:        "totp-secret-marker",
		TotpEnabled:       true,
		Status:            "active",
		SessionsRevokedAt: &suspended,
		SuspendedAt:       &suspended,
		SuspensionReason:  &reason,
		Preferences:       &models.UserPreferences{},
	}

	raw, err := json.Marshal(newPlatformUserView(user))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"11111111-1111-4111-8111-111111111111","email":"ada@example.com","full_name":"Ada Lovelace","status":"active","suspended_at":"2026-10-03T15:04:05Z","totp_enabled":true}`
	if string(raw) != want {
		t.Fatalf("wire = %s", raw)
	}
	for _, marker := range []string{"password-hash-marker", "totp-secret-marker", "operator note", "preferences", "sessions_revoked_at", "password_hash", "totp_secret", "code_hash"} {
		if strings.Contains(string(raw), marker) {
			t.Fatalf("response includes %q", marker)
		}
	}

	page, err := json.Marshal(platformUserViews(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(page) != "[]" {
		t.Fatalf("empty page = %s", page)
	}
}
