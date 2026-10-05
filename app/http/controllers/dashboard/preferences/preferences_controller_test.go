package preferences

import (
	"testing"

	"github.com/macrowallets/waas/app/services/currencies"
	usersvc "github.com/macrowallets/waas/app/services/users"
)

func preferencesControllerDeps() PreferencesControllerDeps {
	return PreferencesControllerDeps{
		Users:      &usersvc.Service{},
		Currencies: &currencies.Service{},
	}
}

func TestNewPreferencesControllerKeepsItsDependencies(t *testing.T) {
	deps := preferencesControllerDeps()
	ctrl := NewPreferencesController(deps)
	if ctrl == nil {
		t.Fatal("NewPreferencesController returned nil")
	}
	if ctrl.users != deps.Users {
		t.Fatal("preferences controller did not keep the users service")
	}
	if ctrl.currencies != deps.Currencies {
		t.Fatal("preferences controller did not keep the currencies service")
	}
}

func TestNewPreferencesControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*PreferencesControllerDeps)
		panic string
	}{
		{
			name:  "users service",
			clear: func(deps *PreferencesControllerDeps) { deps.Users = nil },
			panic: "dashboard preferences controller: users service is required",
		},
		{
			name:  "currencies service",
			clear: func(deps *PreferencesControllerDeps) { deps.Currencies = nil },
			panic: "dashboard preferences controller: currencies service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := preferencesControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewPreferencesController(deps)
			t.Fatal("expected a panic")
		})
	}
}
