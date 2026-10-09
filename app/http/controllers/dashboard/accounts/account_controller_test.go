package accounts

import (
	"testing"

	accountsvc "github.com/macrowallets/waas/app/services/account"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/settings"
)

func accountsControllerDeps() AccountsControllerDeps {
	return AccountsControllerDeps{
		AccountService: &accountsvc.Service{},
		Limits:         &settings.Service{},
		Features:       &featuressvc.Service{},
	}
}

func TestNew_Accounts_ControllerKeepsItsDependencies(t *testing.T) {
	deps := accountsControllerDeps()
	ctrl := NewAccountsController(deps)
	if ctrl == nil {
		t.Fatal("NewAccountsController returned nil")
	}
	if ctrl.accountService != deps.AccountService {
		t.Fatal("accounts controller did not keep the account service")
	}
	if ctrl.limits != deps.Limits {
		t.Fatal("accounts controller did not keep the settings service")
	}
	if ctrl.features != deps.Features {
		t.Fatal("accounts controller did not keep the features service")
	}
}

func TestNew_Accounts_ControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*AccountsControllerDeps)
		panic string
	}{
		{
			name:  "account service",
			clear: func(deps *AccountsControllerDeps) { deps.AccountService = nil },
			panic: "dashboard accounts controller: account service is required",
		},
		{
			name:  "settings service",
			clear: func(deps *AccountsControllerDeps) { deps.Limits = nil },
			panic: "dashboard accounts controller: settings service is required",
		},
		{
			name:  "features service",
			clear: func(deps *AccountsControllerDeps) { deps.Features = nil },
			panic: "dashboard accounts controller: features service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := accountsControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewAccountsController(deps)
			t.Fatal("expected a panic")
		})
	}
}
