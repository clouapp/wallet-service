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

func TestNew_FeatureController_KeepsItsDependencies(t *testing.T) {
	features := &featuressvc.Service{}
	accounts := &stubAccounts{}

	ctrl := NewFeatureController(features, accounts)
	if ctrl == nil {
		t.Fatal("NewFeatureController returned nil")
	}
	if ctrl.features != features {
		t.Fatal("platform features controller did not keep the features service")
	}
	if ctrl.accounts != accounts {
		t.Fatal("platform features controller did not keep the accounts port")
	}
}

func TestNew_FeatureController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		build func()
		panic string
	}{
		{
			name:  "features service",
			build: func() { NewFeatureController(nil, &stubAccounts{}) },
			panic: "platform features controller: features service is required",
		},
		{
			name:  "accounts",
			build: func() { NewFeatureController(&featuressvc.Service{}, nil) },
			panic: "platform features controller: accounts are required",
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
