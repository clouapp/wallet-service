package auth

import (
	"testing"

	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/credentialmail"
	"github.com/macrowallets/waas/app/services/sessions"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

func authControllerDeps() AuthControllerDeps {
	return AuthControllerDeps{
		Users:          &usersvc.Service{},
		Accounts:       &accountsvc.Service{},
		RefreshTokens:  &sessions.RefreshTokens{},
		PasswordResets: &sessions.PasswordResets{},
		Passwords:      &authsvc.Service{},
		TwoFactor:      &authsvc.TwoFactorLogin{},
		Revoker:        &authsvc.SessionRevoker{},
		CredentialMail: &credentialmail.Service{},
	}
}

func TestNewAuthControllerKeepsItsDependencies(t *testing.T) {
	deps := authControllerDeps()
	ctrl := NewAuthController(deps)
	if ctrl == nil {
		t.Fatal("NewAuthController returned nil")
	}
	if ctrl.users != deps.Users {
		t.Fatal("auth controller did not keep the users service")
	}
	if ctrl.accounts != deps.Accounts {
		t.Fatal("auth controller did not keep the account service")
	}
	if ctrl.refreshTokens != deps.RefreshTokens {
		t.Fatal("auth controller did not keep refresh tokens")
	}
	if ctrl.passwordResets != deps.PasswordResets {
		t.Fatal("auth controller did not keep password resets")
	}
	if ctrl.passwords != deps.Passwords {
		t.Fatal("auth controller did not keep the auth service")
	}
	if ctrl.twoFactor != deps.TwoFactor {
		t.Fatal("auth controller did not keep two factor login")
	}
	if ctrl.revoker != deps.Revoker {
		t.Fatal("auth controller did not keep the session revoker")
	}
	if ctrl.credentialMail != deps.CredentialMail {
		t.Fatal("auth controller did not keep credential mail")
	}
}

func TestNewAuthControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*AuthControllerDeps)
		panic string
	}{
		{
			name:  "users service",
			clear: func(deps *AuthControllerDeps) { deps.Users = nil },
			panic: "dashboard auth controller: users service is required",
		},
		{
			name:  "account service",
			clear: func(deps *AuthControllerDeps) { deps.Accounts = nil },
			panic: "dashboard auth controller: account service is required",
		},
		{
			name:  "refresh tokens",
			clear: func(deps *AuthControllerDeps) { deps.RefreshTokens = nil },
			panic: "dashboard auth controller: refresh token service is required",
		},
		{
			name:  "password resets",
			clear: func(deps *AuthControllerDeps) { deps.PasswordResets = nil },
			panic: "dashboard auth controller: password reset service is required",
		},
		{
			name:  "auth service",
			clear: func(deps *AuthControllerDeps) { deps.Passwords = nil },
			panic: "dashboard auth controller: auth service is required",
		},
		{
			name:  "two factor login",
			clear: func(deps *AuthControllerDeps) { deps.TwoFactor = nil },
			panic: "dashboard auth controller: two factor login is required",
		},
		{
			name:  "session revoker",
			clear: func(deps *AuthControllerDeps) { deps.Revoker = nil },
			panic: "dashboard auth controller: session revoker is required",
		},
		{
			name:  "credential mail",
			clear: func(deps *AuthControllerDeps) { deps.CredentialMail = nil },
			panic: "dashboard auth controller: credential mail is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := authControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewAuthController(deps)
			t.Fatal("expected a panic")
		})
	}
}
