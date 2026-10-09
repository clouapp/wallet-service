package settings

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletsettings"
)

func TestNew_SettingsController_RequiresEveryDependency(t *testing.T) {
	defer func() {
		if got := recover(); got != "dashboard wallet settings controller: wallet settings are required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewSettingsController(nil)
	t.Fatal("expected a panic")
}

func TestNew_SettingsController_KeepsItsDependencies(t *testing.T) {
	settings := &walletsettings.Service{}

	ctrl := NewSettingsController(settings)

	if ctrl.settings != settings {
		t.Fatal("settings controller did not keep its dependencies")
	}
}
