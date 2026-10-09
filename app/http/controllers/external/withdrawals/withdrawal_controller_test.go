package withdrawals

import (
	"testing"

	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

func TestNew_WithdrawalController_KeepsItsDependencies(t *testing.T) {
	records := &withdrawalrecords.Records{}
	service := &withdraw.Service{}
	flags := &features.Service{}

	ctrl := NewWithdrawalController(records, service, flags)
	if ctrl == nil {
		t.Fatal("NewWithdrawalController returned nil")
	}
	if ctrl.records != records {
		t.Fatal("withdrawal controller did not keep the withdrawals service")
	}
	if ctrl.service != service {
		t.Fatal("withdrawal controller did not keep the withdrawal service")
	}
	if ctrl.flags != flags {
		t.Fatal("withdrawal controller did not keep the feature flags")
	}
}

func TestNew_WithdrawalController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		build func()
		panic string
	}{
		{
			name:  "withdrawals service",
			build: func() { NewWithdrawalController(nil, &withdraw.Service{}, &features.Service{}) },
			panic: "external withdrawals controller: withdrawals service is required",
		},
		{
			name:  "withdrawal service",
			build: func() { NewWithdrawalController(&withdrawalrecords.Records{}, nil, &features.Service{}) },
			panic: "external withdrawals controller: withdrawal service is required",
		},
		{
			name:  "feature flags",
			build: func() { NewWithdrawalController(&withdrawalrecords.Records{}, &withdraw.Service{}, nil) },
			panic: "external withdrawals controller: feature flags are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			tc.build()
			t.Fatal("expected a panic")
		})
	}
}
