package users

import (
	"bytes"
	"encoding/json"
	"testing"

	userresource "github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/models"
)

func TestMeProfileKeepsTheUserWire(t *testing.T) {
	t.Parallel()

	user := models.User{
		Email:        "ada@example.com",
		PasswordHash: "hidden-password-hash",
		TotpSecret:   "hidden-totp-secret",
		TotpEnabled:  true,
		Status:       "active",
	}
	features := []string{"sweep", "webhooks"}

	got, err := json.Marshal(MeProfile{User: *userresource.UserFrom(&user), Features: features})
	if err != nil {
		t.Fatal(err)
	}
	// The wire the users.me response had when the model carried the tags (before
	// 94fc7b2 moved them to the resource): hidden fields and empty optionals absent.
	want := []byte(`{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","email":"ada@example.com","totp_enabled":true,"status":"active","features":["sweep","webhooks"]}`)
	if bytes.Contains(got, []byte(`"password_hash"`)) || bytes.Contains(got, []byte(`"totp_secret"`)) {
		t.Fatal("hidden key is on the wire")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("me profile wire changed\n got  %s\n want %s", got, want)
	}
}
