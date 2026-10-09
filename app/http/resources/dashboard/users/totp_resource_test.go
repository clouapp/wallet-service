package users_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
)

// sameWire fails when got does not marshal to the bytes of the map the
// handler answered before the resource existed.
func sameWire(t *testing.T, got any, old map[string]any) {
	t.Helper()
	want, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(want) {
		t.Fatalf("wire changed\n got %s\nwant %s", raw, want)
	}
}

func TestTOTP_Resources_KeepTheMapWire(t *testing.T) {
	t.Parallel()

	user := &models.User{ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Email: "ada@example.com", TotpEnabled: true, TotpSecret: "hidden", Status: "active"}

	setup := authsvc.TOTPSecret{Secret: "JBSWY3DPEHPK3PXP", QRURL: "otpauth://totp/Vault:ada"}
	sameWire(t, users.NewTOTPSecret(setup), map[string]any{"secret": setup.Secret, "qr_url": setup.QRURL})

	confirmed := authsvc.TOTPConfirmed{User: user, RecoveryCodes: []string{"AAAA", "BBBB"}}
	sameWire(t, users.NewTOTPConfirmed(confirmed), map[string]any{"user": users.UserFrom(user), "recovery_codes": confirmed.RecoveryCodes})

	disabled := authsvc.TOTPDisabled{User: user, Tokens: authsvc.SessionTokens{AccessToken: "access.jwt", RefreshToken: "REFRESH"}}
	sameWire(t, users.NewTOTPDisabled(disabled), map[string]any{
		"user":          users.UserFrom(user),
		"access_token":  disabled.Tokens.AccessToken,
		"refresh_token": disabled.Tokens.RefreshToken,
	})
}
