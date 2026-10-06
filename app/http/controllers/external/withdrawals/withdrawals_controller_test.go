package withdrawals

import (
	"testing"

	"github.com/redis/go-redis/v9"

	authsvc "github.com/macrowallets/waas/app/services/auth"
	chain "github.com/macrowallets/waas/app/services/chain"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/features"
	usersvc "github.com/macrowallets/waas/app/services/users"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

func withdrawalsControllerDeps() WithdrawalsControllerDeps {
	return WithdrawalsControllerDeps{
		Withdrawals:       &withdrawalrecords.Records{},
		Chains:            &chainsvc.Service{},
		Users:             &usersvc.Service{},
		Transactions:      &walletrecords.Transactions{},
		Registry:          &chain.Registry{},
		WithdrawalService: &withdraw.Service{},
		Passwords:         &authsvc.Service{},
		Flags:             &features.Service{},
		Events:            &withdrawalevents.Publisher{},
		Redis:             &redis.Client{},
		SecondFactor:      &authsvc.SecondFactorVerifier{},
	}
}

func TestNew_Withdrawals_ControllerKeepsItsDependencies(t *testing.T) {
	deps := withdrawalsControllerDeps()
	ctrl := NewWithdrawalsController(deps)
	if ctrl == nil {
		t.Fatal("NewWithdrawalsController returned nil")
	}
	if ctrl.withdrawals != deps.Withdrawals {
		t.Fatal("withdrawals controller did not keep the withdrawals service")
	}
	if ctrl.chains != deps.Chains {
		t.Fatal("withdrawals controller did not keep the chains service")
	}
	if ctrl.users != deps.Users {
		t.Fatal("withdrawals controller did not keep the users service")
	}
	if ctrl.transactions != deps.Transactions {
		t.Fatal("withdrawals controller did not keep the transactions service")
	}
	if ctrl.registry != deps.Registry {
		t.Fatal("withdrawals controller did not keep the chain registry")
	}
	if ctrl.withdrawalService != deps.WithdrawalService {
		t.Fatal("withdrawals controller did not keep the withdrawal service")
	}
	if ctrl.passwords != deps.Passwords {
		t.Fatal("withdrawals controller did not keep the auth service")
	}
	if ctrl.flags != deps.Flags {
		t.Fatal("withdrawals controller did not keep the feature flags")
	}
	if ctrl.events != deps.Events {
		t.Fatal("withdrawals controller did not keep the withdrawal events publisher")
	}
	if ctrl.redis != deps.Redis {
		t.Fatal("withdrawals controller did not keep the redis client")
	}
	if ctrl.secondFactor != deps.SecondFactor {
		t.Fatal("withdrawals controller did not keep the second factor verifier")
	}
}

func TestNew_Withdrawals_ControllerAllowsNilEventsAndRedis(t *testing.T) {
	deps := withdrawalsControllerDeps()
	deps.Events = nil
	deps.Redis = nil
	ctrl := NewWithdrawalsController(deps)
	if ctrl == nil {
		t.Fatal("NewWithdrawalsController returned nil")
	}
	if ctrl.events != nil || ctrl.redis != nil {
		t.Fatal("withdrawals controller did not keep nil events and redis")
	}
	if ctrl.withdrawals != deps.Withdrawals || ctrl.transactions != deps.Transactions || ctrl.flags != deps.Flags || ctrl.secondFactor != deps.SecondFactor {
		t.Fatal("withdrawals controller dropped a required dependency")
	}
}

func TestNew_Withdrawals_ControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*WithdrawalsControllerDeps)
		panic string
	}{
		{
			name:  "withdrawals service",
			clear: func(deps *WithdrawalsControllerDeps) { deps.Withdrawals = nil },
			panic: "external withdrawals controller: withdrawals service is required",
		},
		{
			name:  "chains service",
			clear: func(deps *WithdrawalsControllerDeps) { deps.Chains = nil },
			panic: "external withdrawals controller: chains service is required",
		},
		{
			name:  "users service",
			clear: func(deps *WithdrawalsControllerDeps) { deps.Users = nil },
			panic: "external withdrawals controller: users service is required",
		},
		{
			name:  "transactions service",
			clear: func(deps *WithdrawalsControllerDeps) { deps.Transactions = nil },
			panic: "external withdrawals controller: transactions service is required",
		},
		{
			name:  "chain registry",
			clear: func(deps *WithdrawalsControllerDeps) { deps.Registry = nil },
			panic: "external withdrawals controller: chain registry is required",
		},
		{
			name:  "withdrawal service",
			clear: func(deps *WithdrawalsControllerDeps) { deps.WithdrawalService = nil },
			panic: "external withdrawals controller: withdrawal service is required",
		},
		{
			name:  "auth service",
			clear: func(deps *WithdrawalsControllerDeps) { deps.Passwords = nil },
			panic: "external withdrawals controller: auth service is required",
		},
		{
			name:  "feature flags",
			clear: func(deps *WithdrawalsControllerDeps) { deps.Flags = nil },
			panic: "external withdrawals controller: feature flags are required",
		},
		{
			name:  "second factor verifier",
			clear: func(deps *WithdrawalsControllerDeps) { deps.SecondFactor = nil },
			panic: "external withdrawals controller: second factor verifier is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := withdrawalsControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewWithdrawalsController(deps)
			t.Fatal("expected a panic")
		})
	}
}
