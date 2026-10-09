package accounts

import (
	"testing"

	accountsvc "github.com/macrowallets/waas/app/services/account"
)

func TestNew_Account_ControllerKeepsTheAccountService(t *testing.T) {
	accounts := &accountsvc.Service{}
	if ctrl := NewAccountController(accounts); ctrl == nil || ctrl.accounts != accounts {
		t.Fatal("account controller did not keep the account service")
	}

	defer func() {
		if got := recover(); got != "dashboard account controller: account service is required" {
			t.Fatalf("panic = %v", got)
		}
	}()
	NewAccountController(nil)
	t.Fatal("expected a panic")
}
