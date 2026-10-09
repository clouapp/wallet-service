package controllers_test

import (
	"context"
	"github.com/macrowallets/waas/app/http/controllers"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/services/features"
	sweepsvc "github.com/macrowallets/waas/app/services/sweep"
)

type sweepServiceStub struct{}

func (*sweepServiceStub) PlanForWithdrawal(context.Context, uuid.UUID, string, *big.Int, string, uuid.UUID) (*sweepsvc.Plan, error) {
	return nil, nil
}

func (*sweepServiceStub) ExecutePlan(context.Context, *sweepsvc.Plan, sweepsvc.SigningCredentials, uuid.UUID, string, string) (*sweepsvc.Result, error) {
	return nil, nil
}

func (*sweepServiceStub) ConsolidateAll(context.Context, uuid.UUID, string, string, uuid.UUID) (*sweepsvc.Result, error) {
	return nil, nil
}

func (*sweepServiceStub) RefreshGasStatus(context.Context, uuid.UUID) (*sweepsvc.GasStatus, error) {
	return nil, nil
}

func (*sweepServiceStub) LoadLimits(context.Context, uuid.UUID) (*sweepsvc.Limits, error) {
	return nil, nil
}

func TestNew_SweepHandler_RequiresEveryDependencyAndNamesTheSurface(t *testing.T) {
	cases := []struct {
		name   string
		sweeps sweepsvc.Service
		flags  *features.Service
		panic  string
	}{
		{"sweep service", nil, &features.Service{}, "test sweep controller: sweep service is required"},
		{"feature flags", &sweepServiceStub{}, nil, "test sweep controller: feature flags are required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			controllers.NewSweepHandler("test", tc.sweeps, tc.flags)
			t.Fatal("expected a panic")
		})
	}
}

func TestNew_SweepHandler_AcceptsCompleteDependencies(t *testing.T) {
	if controllers.NewSweepHandler("test", &sweepServiceStub{}, &features.Service{}) == nil {
		t.Fatal("NewSweepHandler returned nil")
	}
}
