package settings

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/walletsettings"
)

func TestNew_SettingsController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name        string
		settings    *walletsettings.Service
		memberships *walletrecords.Memberships
		panic       string
	}{
		{"wallet settings", nil, &walletrecords.Memberships{}, "dashboard wallet settings controller: wallet settings are required"},
		{"wallet memberships", &walletsettings.Service{}, nil, "dashboard wallet settings controller: wallet memberships are required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewSettingsController(tc.settings, tc.memberships)
			t.Fatal("expected a panic")
		})
	}
}

func TestNew_SettingsController_KeepsItsDependencies(t *testing.T) {
	settings, memberships := &walletsettings.Service{}, &walletrecords.Memberships{}

	ctrl := NewSettingsController(settings, memberships)

	if ctrl.settings != settings || ctrl.memberships != memberships {
		t.Fatal("settings controller did not keep its dependencies")
	}
}
