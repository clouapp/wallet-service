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
	FindByID(ctx context.Context, id string) (*models.Chain, error)
}

// TokenCatalog is the token reads the dashboard chain handlers perform.
type TokenCatalog interface {
	FindByChainID(ctx context.Context, chainID string) ([]models.Token, error)
}

// ResourceCatalog is the chain-resource reads the dashboard chain handlers perform.
type ResourceCatalog interface {
	FindByChainID(ctx context.Context, chainID string) ([]models.ChainResource, error)
}

// Service lists chains for an account environment and reads one chain's
// tokens and resources. Tokens and resources stay nil for callers that only
// list chains.
type Service struct {
	chains    Catalog
	tokens    TokenCatalog
	resources ResourceCatalog
}

// NewService builds a chain catalogue service. Tokens and resources are
// attached with WithTokens and WithResources when a handler reads them.
func NewService(chains Catalog) *Service {
	return &Service{chains: chains}
}

// WithTokens attaches the token catalogue. It returns the same service.
func (s *Service) WithTokens(tokens TokenCatalog) *Service {
	if s == nil {
		return nil
	}
	s.tokens = tokens
	return s
}

// WithResources attaches the chain-resource catalogue. It returns the same service.
func (s *Service) WithResources(resources ResourceCatalog) *Service {
	if s == nil {
		return nil
	}
	s.resources = resources
	return s
}

// FindByID returns one chain. A missing row is the store's not-found error.
func (s *Service) FindByID(ctx context.Context, id string) (*models.Chain, error) {
	if ctx == nil {
		return nil, fmt.Errorf("find chain: context is required")
	}
	if s == nil || s.chains == nil {
		return nil, fmt.Errorf("chains service: chains repository is required")
	}
	return s.chains.FindByID(ctx, id)
}

// FindTokens returns the tokens of one chain.
func (s *Service) FindTokens(ctx context.Context, chainID string) ([]models.Token, error) {
	if ctx == nil {
		return nil, fmt.Errorf("list chain tokens: context is required")
	}
	if s == nil || s.tokens == nil {
		return nil, fmt.Errorf("chains service: tokens repository is required")
	}
	return s.tokens.FindByChainID(ctx, chainID)
}

// FindResources returns the resources of one chain.
func (s *Service) FindResources(ctx context.Context, chainID string) ([]models.ChainResource, error) {
	if ctx == nil {
		return nil, fmt.Errorf("list chain resources: context is required")
	}
	if s == nil || s.resources == nil {
		return nil, fmt.Errorf("chains service: chain resources repository is required")
	}
	return s.resources.FindByChainID(ctx, chainID)
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
