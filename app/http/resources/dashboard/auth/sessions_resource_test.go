package auth_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/resources/dashboard/auth"
	"github.com/macrowallets/waas/app/http/resources/dashboard/users"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/account"
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

// legacyAccounts is how the handler built the accounts of a sign-in answer
// before the resource existed.
func legacyAccounts(signIn account.SignInAccounts) ([]map[string]interface{}, map[string]interface{}) {
	var accounts []map[string]interface{}
	var defaultAccount map[string]interface{}
	for _, member := range signIn.Accounts {
		entry := map[string]interface{}{
			"id":                member.Account.ID,
			"name":              member.Account.Name,
			"environment":       member.Account.Environment,
			"linked_account_id": member.Account.LinkedAccountID,
			"status":            member.Account.Status,
			"role":              member.Role,
		}
		accounts = append(accounts, entry)
		if member.Account.ID == signIn.DefaultID {
			defaultAccount = entry
		}
	}
	return accounts, defaultAccount
}

func signInFixtures() map[string]account.SignInAccounts {
	prod, test := uuid.MustParse("11111111-1111-4111-8111-111111111111"), uuid.MustParse("22222222-2222-4222-8222-222222222222")
	accounts := []account.SignInAccount{
		{Account: models.Account{ID: prod, Name: "Acme", Environment: "prod", Status: "active", LinkedAccountID: &test}, Role: "owner"},
		{Account: models.Account{ID: test, Name: "Acme [test]", Environment: "test", Status: "frozen"}, Role: "admin"},
	}
	return map[string]account.SignInAccounts{
		"default":    {Accounts: accounts, DefaultID: prod},
		"no default": {Accounts: accounts},
		"none":       {},
	}
}

func TestSigned_In_KeepsTheMapWire(t *testing.T) {
	t.Parallel()

	user := &models.User{ID: uuid.MustParse("33333333-3333-4333-8333-333333333333"), Email: "ada@example.com", Status: "active", TotpSecret: "hidden"}
	for name, signIn := range signInFixtures() {
		t.Run(name, func(t *testing.T) {
			tokens := authsvc.SessionTokens{AccessToken: "access.jwt", RefreshToken: "REFRESH"}
			accounts, defaultAccount := legacyAccounts(signIn)
			old := map[string]any{
				"access_token":  tokens.AccessToken,
				"refresh_token": tokens.RefreshToken,
				"user":          users.UserFrom(user),
				"accounts":      accounts,
			}
			if defaultAccount != nil {
				old["account_id"] = defaultAccount["id"]
				old["account"] = defaultAccount
			}
			sameWire(t, auth.NewSignedIn(authsvc.SignedIn{User: user, Tokens: tokens, Accounts: signIn}), old)

			registered := map[string]any{
				"access_token": tokens.AccessToken,
				"user":         users.UserFrom(user),
				"accounts":     accounts,
			}
			if defaultAccount != nil {
				registered["account_id"] = defaultAccount["id"]
				registered["account"] = defaultAccount
			}
			sameWire(t, auth.NewRegistered(authsvc.Registered{User: user, Accounts: signIn, AccessToken: tokens.AccessToken}), registered)
		})
	}
}

func TestTwo_Factor_ChallengeAndTheMessagesKeepTheMapWire(t *testing.T) {
	t.Parallel()

	challenge := authsvc.TwoFactorChallenge{Token: "challenge-token", ExpiresIn: 5 * time.Minute}
	sameWire(t, auth.NewTwoFactorChallenge(challenge), map[string]any{
		"requires_2fa":    true,
		"challenge_token": challenge.Token,
		"expires_in":      int(challenge.ExpiresIn.Seconds()),
	})
	tokens := authsvc.SessionTokens{AccessToken: "access.jwt", RefreshToken: "REFRESH"}
	sameWire(t, auth.NewSession(tokens), map[string]any{"access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken})
	sameWire(t, auth.NewResetRequested(), map[string]any{"message": "if that address is registered, you will receive a reset link"})
	sameWire(t, auth.NewPasswordReset(), map[string]any{"message": "password reset successfully"})
}
