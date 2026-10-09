package auth_test

import (
	"encoding/json"
	"testing"

	"github.com/macrowallets/waas/app/http/resources/dashboard/auth"
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

func TestPassword_Changed_KeepsTheMapWire(t *testing.T) {
	t.Parallel()

	tokens := authsvc.SessionTokens{AccessToken: "access.jwt", RefreshToken: "REFRESH"}
	sameWire(t, auth.NewPasswordChanged(tokens), map[string]any{
		"message":       "password updated successfully",
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
	})
}
