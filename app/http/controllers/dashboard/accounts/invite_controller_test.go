package accounts

import (
	"testing"

	accountsvc "github.com/macrowallets/waas/app/services/account"
)

func TestNew_Invite_ControllerKeepsTheAccountService(t *testing.T) {
	accounts := &accountsvc.Service{}
	if ctrl := NewInviteController(accounts); ctrl == nil || ctrl.accounts != accounts {
		t.Fatal("invite controller did not keep the account service")
	}

	defer func() {
		if got := recover(); got != "dashboard invite controller: account service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewInviteController(nil)
	t.Fatal("expected a panic")
}
