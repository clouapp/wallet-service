package wallets

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletrecords"
)

func whitelistControllerDeps() WhitelistControllerDeps {
	return WhitelistControllerDeps{
		Entries:     &walletrecords.Whitelist{},
		Memberships: &walletrecords.Memberships{},
	}
}

func TestNew_Whitelist_ControllerKeepsItsDependencies(t *testing.T) {
	deps := whitelistControllerDeps()
	ctrl := NewWhitelistController(deps)
	if ctrl == nil {
		t.Fatal("NewWhitelistController returned nil")
	}
	if ctrl.entries != deps.Entries {
		t.Fatal("whitelist controller did not keep the whitelist service")
	}
	if ctrl.memberships != deps.Memberships {
		t.Fatal("whitelist controller did not keep the wallet memberships")
	}
}

func TestNew_Whitelist_ControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*WhitelistControllerDeps)
		panic string
	}{
		{
			name:  "whitelist service",
			clear: func(deps *WhitelistControllerDeps) { deps.Entries = nil },
			panic: "dashboard whitelist controller: whitelist service is required",
		},
		{
			name:  "wallet memberships",
			clear: func(deps *WhitelistControllerDeps) { deps.Memberships = nil },
			panic: "dashboard whitelist controller: wallet memberships are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := whitelistControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewWhitelistController(deps)
			t.Fatal("expected a panic")
		})
	}
}
