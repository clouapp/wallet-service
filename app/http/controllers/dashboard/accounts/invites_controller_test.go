package accounts

import (
	"strings"
	"testing"

	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/credentialmail"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

func invitesControllerDeps() InvitesControllerDeps {
	return InvitesControllerDeps{
		Accounts:       &accountsvc.Service{},
		Users:          &usersvc.Service{},
		CredentialMail: &credentialmail.Service{},
	}
}

func TestNewInvitesControllerKeepsItsDependencies(t *testing.T) {
	deps := invitesControllerDeps()
	ctrl := NewInvitesController(deps)
	if ctrl == nil {
		t.Fatal("NewInvitesController returned nil")
	}
	if ctrl.accounts != deps.Accounts {
		t.Fatal("invites controller did not keep the account service")
	}
	if ctrl.users != deps.Users {
		t.Fatal("invites controller did not keep the user service")
	}
	if ctrl.credentialMail != deps.CredentialMail {
		t.Fatal("invites controller did not keep credential mail")
	}
}

func TestNewInvitesControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*InvitesControllerDeps)
		panic string
	}{
		{
			name:  "account service",
			clear: func(deps *InvitesControllerDeps) { deps.Accounts = nil },
			panic: "dashboard invites controller: account service is required",
		},
		{
			name:  "user service",
			clear: func(deps *InvitesControllerDeps) { deps.Users = nil },
			panic: "dashboard invites controller: user service is required",
		},
		{
			name:  "credential mail",
			clear: func(deps *InvitesControllerDeps) { deps.CredentialMail = nil },
			panic: "dashboard invites controller: credential mail is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := invitesControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewInvitesController(deps)
			t.Fatal("expected a panic")
		})
	}
}

func TestFrontendBaseURLUsesOnlyTheEnv(t *testing.T) {
	t.Setenv(frontendURLEnv, "")
	base, err := frontendBaseURL()
	if err == nil || base != "" {
		t.Fatal("missing APP_FRONTEND_URL was accepted")
	}
	if strings.Contains(base, "localhost") || strings.Contains(base, "vault.app") || strings.Contains(err.Error(), "http") {
		t.Fatal("missing APP_FRONTEND_URL produced a host")
	}

	t.Setenv(frontendURLEnv, "   ")
	if _, err := frontendBaseURL(); err == nil {
		t.Fatal("blank APP_FRONTEND_URL was accepted")
	}

	t.Setenv(frontendURLEnv, "  https://wallet.example/app  ")
	base, err = frontendBaseURL()
	if err != nil || base != "https://wallet.example/app" {
		t.Fatal("APP_FRONTEND_URL was not the invite link base")
	}
}
