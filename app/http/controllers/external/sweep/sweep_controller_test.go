package sweep

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

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

func sweepControllerDeps() SweepControllerDeps {
	return SweepControllerDeps{
		Sweeps: &sweepServiceStub{},
		Redis:  &redis.Client{},
		Flags:  &features.Service{},
	}
}

func TestNewSweepControllerKeepsItsDependencies(t *testing.T) {
	deps := sweepControllerDeps()
	ctrl := NewSweepController(deps)
	if ctrl == nil {
		t.Fatal("NewSweepController returned nil")
	}
	if ctrl.sweeps != deps.Sweeps {
		t.Fatal("sweep controller did not keep the sweep service")
	}
	if ctrl.redis != deps.Redis {
		t.Fatal("sweep controller did not keep the redis client")
	}
	if ctrl.flags != deps.Flags {
		t.Fatal("sweep controller did not keep the feature flags")
	}
}

func TestNewSweepControllerAllowsNilRedis(t *testing.T) {
	deps := sweepControllerDeps()
	deps.Redis = nil
	ctrl := NewSweepController(deps)
	if ctrl == nil {
		t.Fatal("NewSweepController returned nil")
	}
	if ctrl.redis != nil {
		t.Fatal("sweep controller did not keep a nil redis client")
	}
	if ctrl.sweeps != deps.Sweeps || ctrl.flags != deps.Flags {
		t.Fatal("sweep controller dropped a required dependency")
	}
}

func TestNewSweepControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*SweepControllerDeps)
		panic string
	}{
		{
			name:  "sweep service",
			clear: func(deps *SweepControllerDeps) { deps.Sweeps = nil },
			panic: "external sweep controller: sweep service is required",
		},
		{
			name:  "feature flags",
			clear: func(deps *SweepControllerDeps) { deps.Flags = nil },
			panic: "external sweep controller: feature flags are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := sweepControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewSweepController(deps)
			t.Fatal("expected a panic")
		})
	}
}
