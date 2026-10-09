package accounts

import (
	"testing"

	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/app/services/apitoken"
)

func TestNew_Token_ControllerRequiresBothServices(t *testing.T) {
	cases := []struct {
		name     string
		accounts *accountsvc.Service
		tokens   *apitoken.Service
		panic    string
	}{
		{name: "account service", tokens: &apitoken.Service{}, panic: "dashboard token controller: account service is required"},
		{name: "token service", accounts: &accountsvc.Service{}, panic: "dashboard token controller: token service is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewTokenController(tc.accounts, tc.tokens)
			t.Fatal("expected a panic")
		})
	}
}
