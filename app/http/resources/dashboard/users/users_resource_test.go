package users_test

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/models"
)

func TestUserFromMatchesTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	accountID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	display := false
	reason := "hidden-reason"
	revoked := time.Date(2024, 5, 6, 7, 8, 11, 0, time.UTC)
	full := &models.User{
		ID:                  id,
		Email:               "ada@example.com",
		PasswordHash:        "hidden-password-hash",
		FullName:            "Ada Lovelace",
		TotpSecret:          "hidden-totp-secret",
		TotpEnabled:         true,
		Status:              "active",
		DefaultAccountID:    &accountID,
		Preferences:         &models.UserPreferences{PreferredFiatCode: "BRL", DisplayInFiat: &display},
		TotpLastUsedCounter: 42,
		SessionsRevokedAt:   &revoked,
		SuspendedAt:         &revoked,
		SuspensionReason:    &reason,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	fiatOnly := &models.User{Email: "ada@example.com", Preferences: &models.UserPreferences{PreferredFiatCode: "EUR"}}
	displayOnly := &models.User{Email: "ada@example.com", Preferences: &models.UserPreferences{DisplayInFiat: &display}}
	emptyPrefs := &models.User{Email: "ada@example.com", Preferences: &models.UserPreferences{}}

	zero := &models.User{}
	for _, user := range []*models.User{nil, zero, full, fiatOnly, displayOnly, emptyPrefs} {
		assertSameUserWire(t, user)
	}
}

func assertSameUserWire(t *testing.T, user *models.User) {
	t.Helper()

	want, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(users.UserFrom(user))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"password_hash",
		"totp_secret",
		"totp_last_used_counter",
		"sessions_revoked_at",
		"suspended_at",
		"suspension_reason",
	} {
		if bytes.Contains(got, []byte(`"`+key+`"`)) {
			t.Fatalf("hidden key %s is on the wire", key)
		}
	}
	if bytes.Equal(got, want) {
		return
	}
	t.Fatalf("user wire changed\n model %s\n resource keys %s", want, jsonKeys(t, got))
}

func jsonKeys(t *testing.T, raw []byte) string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encoded, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
