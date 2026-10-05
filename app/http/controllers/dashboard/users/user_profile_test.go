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
	type modelProfile struct {
		models.User
		Features []string `json:"features"`
	}
	want, err := json.Marshal(modelProfile{User: user, Features: features})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(`"password_hash"`)) || bytes.Contains(got, []byte(`"totp_secret"`)) {
		t.Fatal("hidden key is on the wire")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("me profile wire changed\n model %s", want)
	}
}
