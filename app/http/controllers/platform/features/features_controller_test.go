package features

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	featuressvc "github.com/macrowallets/waas/app/services/features"
)

type stubAccounts struct{}

func (*stubAccounts) FindByID(context.Context, uuid.UUID) (*models.Account, error) {
	return nil, nil
}

func featuresControllerDeps() FeaturesControllerDeps {
	return FeaturesControllerDeps{
		Features: &featuressvc.Service{},
		Accounts: &stubAccounts{},
	}
}

func TestNew_Features_ControllerKeepsItsDependencies(t *testing.T) {
	deps := featuresControllerDeps()
	ctrl := NewFeaturesController(deps)
	if ctrl == nil {
		t.Fatal("NewFeaturesController returned nil")
	}
	if ctrl.features != deps.Features {
		t.Fatal("platform features controller did not keep the features service")
	}
	if ctrl.accounts != deps.Accounts {
		t.Fatal("platform features controller did not keep the accounts port")
	}
}

func TestNew_Features_ControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*FeaturesControllerDeps)
		panic string
	}{
		{
			name:  "features service",
			clear: func(deps *FeaturesControllerDeps) { deps.Features = nil },
			panic: "platform features controller: features service is required",
		},
		{
			name:  "accounts",
			clear: func(deps *FeaturesControllerDeps) { deps.Accounts = nil },
			panic: "platform features controller: accounts are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := featuresControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewFeaturesController(deps)
			t.Fatal("expected a panic")
		})
	}
}
