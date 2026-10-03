package chains

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/app/models"
)

// Catalog is the chain reads this service performs.
type Catalog interface {
	FindActive(ctx context.Context) ([]models.Chain, error)
	FindByTestnet(ctx context.Context, isTestnet bool) ([]models.Chain, error)
}

// Service lists chains for an account environment.
type Service struct {
	chains Catalog
}

// NewService builds a chain catalogue service.
func NewService(chains Catalog) *Service {
	return &Service{chains: chains}
}

// ListForEnvironment returns the chains the external list handler serializes.
// prod and test select active chains of that network kind. Any other
// environment, including a missing one, returns every active chain.
func (s *Service) ListForEnvironment(ctx context.Context, environment string) ([]models.Chain, error) {
	if ctx == nil {
		return nil, fmt.Errorf("list chains: context is required")
	}
	if s == nil || s.chains == nil {
		return nil, fmt.Errorf("chains service: chains repository is required")
	}
	if environment == models.EnvironmentProd || environment == models.EnvironmentTest {
		return s.chains.FindByTestnet(ctx, environment == models.EnvironmentTest)
	}
	return s.chains.FindActive(ctx)
}
