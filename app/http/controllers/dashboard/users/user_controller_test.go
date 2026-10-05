package users

import (
	"testing"

	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/sessions"
	"github.com/macrowallets/waas/app/services/settings"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

func usersControllerDeps() UsersControllerDeps {
	return UsersControllerDeps{
		Users:        &usersvc.Service{},
		Accounts:     &accountsvc.Service{},
		Passwords:    &authsvc.Service{},
		Refresh:      &sessions.RefreshTokens{},
		SecondFactor: &authsvc.SecondFactorVerifier{},
		Revoker:      &authsvc.SessionRevoker{},
		Features:     &featuressvc.Service{},
		Limits:       &settings.Service{},
	}
}

func TestNewUsersControllerKeepsItsDependencies(t *testing.T) {
	deps := usersControllerDeps()
	ctrl := NewUsersController(deps)
	if ctrl == nil {
		t.Fatal("NewUsersController returned nil")
	}
	if ctrl.users != deps.Users {
		t.Fatal("users controller did not keep the users service")
	}
	if ctrl.accounts != deps.Accounts {
		t.Fatal("users controller did not keep the account service")
	}
	if ctrl.passwords != deps.Passwords {
		t.Fatal("users controller did not keep the auth service")
	}
	if ctrl.refresh != deps.Refresh {
		t.Fatal("users controller did not keep refresh tokens")
	}
	if ctrl.secondFactor != deps.SecondFactor {
		t.Fatal("users controller did not keep the second factor verifier")
	}
	if ctrl.revoker != deps.Revoker {
		t.Fatal("users controller did not keep the session revoker")
	}
	if ctrl.features != deps.Features {
		t.Fatal("users controller did not keep feature flags")
	}
	if ctrl.limits != deps.Limits {
		t.Fatal("users controller did not keep the settings service")
	}
}

func TestNewUsersControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*UsersControllerDeps)
		panic string
	}{
		{
			name:  "users service",
			clear: func(deps *UsersControllerDeps) { deps.Users = nil },
			panic: "dashboard users controller: users service is required",
		},
		{
			name:  "account service",
			clear: func(deps *UsersControllerDeps) { deps.Accounts = nil },
			panic: "dashboard users controller: account service is required",
		},
		{
			name:  "auth service",
			clear: func(deps *UsersControllerDeps) { deps.Passwords = nil },
			panic: "dashboard users controller: auth service is required",
		},
		{
			name:  "refresh tokens",
			clear: func(deps *UsersControllerDeps) { deps.Refresh = nil },
			panic: "dashboard users controller: refresh tokens are required",
		},
		{
			name:  "second factor verifier",
			clear: func(deps *UsersControllerDeps) { deps.SecondFactor = nil },
			panic: "dashboard users controller: second factor verifier is required",
		},
		{
			name:  "session revoker",
			clear: func(deps *UsersControllerDeps) { deps.Revoker = nil },
			panic: "dashboard users controller: session revoker is required",
		},
		{
			name:  "feature flags",
			clear: func(deps *UsersControllerDeps) { deps.Features = nil },
			panic: "dashboard users controller: feature flags are required",
		},
		{
			name:  "settings service",
			clear: func(deps *UsersControllerDeps) { deps.Limits = nil },
			panic: "dashboard users controller: settings service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := usersControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewUsersController(deps)
			t.Fatal("expected a panic")
		})
	}
}
