package preferences

import (
	"testing"

	usersvc "github.com/macrowallets/waas/app/services/users"
)

func TestNew_Preferences_ControllerKeepsTheUsersService(t *testing.T) {
	users := &usersvc.Service{}
	if ctrl := NewPreferencesController(users); ctrl == nil || ctrl.users != users {
		t.Fatal("preferences controller did not keep the users service")
	}

	defer func() {
		if got := recover(); got != "dashboard preferences controller: users service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewPreferencesController(nil)
	t.Fatal("expected a panic")
}
