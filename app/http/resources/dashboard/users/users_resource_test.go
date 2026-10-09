package users_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/models"
)

func TestUser_From_MatchesTheModelWire(t *testing.T) {
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

	got, err := json.Marshal(users.UserFrom(user))
	if err != nil {
		t.Fatal(err)
	}
	if user == nil {
		if string(got) != "null" {
			t.Fatalf("nil user wire = %s", got)
		}
		return
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
	if user.PasswordHash != "" && bytes.Contains(got, []byte(user.PasswordHash)) {
		t.Fatal("password hash is on the wire")
	}
	if user.TotpSecret != "" && bytes.Contains(got, []byte(user.TotpSecret)) {
		t.Fatal("totp secret is on the wire")
	}
	if user.SuspensionReason != nil && *user.SuspensionReason != "" && bytes.Contains(got, []byte(*user.SuspensionReason)) {
		t.Fatal("suspension reason is on the wire")
	}

	var body map[string]any
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != user.ID.String() {
		t.Fatalf("id wire = %v", body["id"])
	}
	if body["email"] != user.Email {
		t.Fatalf("email wire = %v", body["email"])
	}
	if body["totp_enabled"] != user.TotpEnabled {
		t.Fatalf("totp_enabled wire = %v", body["totp_enabled"])
	}
	if body["status"] != user.Status {
		t.Fatalf("status wire = %v", body["status"])
	}
	if user.FullName == "" {
		if _, ok := body["full_name"]; ok {
			t.Fatal("empty full_name is on the wire")
		}
	} else if body["full_name"] != user.FullName {
		t.Fatalf("full_name wire = %v", body["full_name"])
	}
	if user.DefaultAccountID == nil {
		if _, ok := body["default_account_id"]; ok {
			t.Fatal("nil default_account_id is on the wire")
		}
	} else if body["default_account_id"] != user.DefaultAccountID.String() {
		t.Fatalf("default_account_id wire = %v", body["default_account_id"])
	}
	if user.Preferences == nil {
		if _, ok := body["preferences"]; ok {
			t.Fatal("nil preferences are on the wire")
		}
		return
	}
	prefs, ok := body["preferences"].(map[string]any)
	if !ok {
		t.Fatalf("preferences wire = %T", body["preferences"])
	}
	if user.Preferences.PreferredFiatCode == "" {
		if _, ok := prefs["preferred_fiat_code"]; ok {
			t.Fatal("empty preferred_fiat_code is on the wire")
		}
	} else if prefs["preferred_fiat_code"] != user.Preferences.PreferredFiatCode {
		t.Fatalf("preferred_fiat_code wire = %v", prefs["preferred_fiat_code"])
	}
	if user.Preferences.DisplayInFiat == nil {
		if _, ok := prefs["display_in_fiat"]; ok {
			t.Fatal("nil display_in_fiat is on the wire")
		}
	} else if prefs["display_in_fiat"] != *user.Preferences.DisplayInFiat {
		t.Fatalf("display_in_fiat wire = %v", prefs["display_in_fiat"])
	}
}

// TestUser_HoldsOnlyWireFields: the resource has no field for a secret or an
// internal column, so nothing depends on a json:"-" tag to keep it off the wire.
func TestUser_HoldsOnlyWireFields(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(users.User{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Tag.Get("json") == "-" || field.Tag.Get("json") == "" {
			t.Errorf("field %s is not a wire field", field.Name)
		}
	}
}
