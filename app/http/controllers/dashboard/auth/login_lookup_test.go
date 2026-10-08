package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/http/resources"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/credentialmail"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

type lookupUsers struct {
	usersvc.Store
	user *models.User
	err  error
}

func (u lookupUsers) FindByEmail(context.Context, string) (*models.User, error) {
	return u.user, u.err
}

func loginWith(t *testing.T, store lookupUsers) *recordingResponse {
	t.Helper()
	previous := foundation.App
	foundation.App = quietApp{}
	t.Cleanup(func() { foundation.App = previous })

	ctrl := NewAuthController(AuthControllerDeps{
		Users:          usersvc.NewService(usersvc.Deps{Store: store}),
		Accounts:       accountsvc.NewService(accountsvc.Deps{}),
		RefreshTokens:  &sessions.RefreshTokens{},
		PasswordResets: &sessions.PasswordResets{},
		Passwords:      authsvc.NewServiceWithHasher(testHasher()),
		TwoFactor:      &authsvc.TwoFactorLogin{},
		Revoker:        &authsvc.SessionRevoker{},
		CredentialMail: &credentialmail.Service{},
	})
	response := &recordingResponse{}
	ctrl.Login(&recordingContext{
		base:     context.Background(),
		request:  &recordingRequest{email: "someone@example.com", password: "whatever"},
		response: response,
	})
	return response
}

func errorCode(t *testing.T, response *recordingResponse) string {
	t.Helper()
	if envelope, ok := response.body.(resources.ErrorEnvelope); ok {
		return envelope.Error.Code
	}
	var decoded resources.ErrorEnvelope
	if err := json.Unmarshal(response.raw, &decoded); err != nil {
		t.Fatalf("body is not the error envelope: %v (%#v)", err, response.body)
	}
	return decoded.Error.Code
}

func TestLogin_UnknownEmail_IsUnauthorized(t *testing.T) {
	for name, store := range map[string]lookupUsers{
		"not found":  {err: fmt.Errorf("find user by email: %w", models.ErrRepositoryNotFound)},
		"nil result": {},
	} {
		t.Run(name, func(t *testing.T) {
			response := loginWith(t, store)
			if response.status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.status)
			}
			if code := errorCode(t, response); code != responses.CodeUnauthorized {
				t.Fatalf("code = %q, want %q", code, responses.CodeUnauthorized)
			}
		})
	}
}

// A failed user lookup is our outage, not a bad credential: the caller must
// not be told "invalid credentials" and a client must not clear a good password.
func TestLogin_UserLookupFailure_IsInternalError(t *testing.T) {
	response := loginWith(t, lookupUsers{err: errors.New("connection refused")})
	if response.status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.status)
	}
	if code := errorCode(t, response); code != responses.CodeInternal {
		t.Fatalf("code = %q, want %q", code, responses.CodeInternal)
	}
	if strings.Contains(string(response.raw), "connection refused") {
		t.Fatal("the body carries the cause")
	}
}
