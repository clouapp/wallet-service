package chains

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
)

var (
	// ErrChainNotFound is the answer for a chain id the catalogue does not hold.
	ErrChainNotFound = errors.New("chain not found")
	// ErrChainLookup wraps a catalogue failure, so a caller can tell our outage
	// from a chain that does not exist. The cause stays on the error chain.
	ErrChainLookup = errors.New("chain lookup failed")
	// ErrTokensUnavailable and ErrResourcesUnavailable wrap a failed read of the
	// chain's tokens or resources, for the callers that serve only that list.
	ErrTokensUnavailable    = errors.New("chain tokens unavailable")
	ErrResourcesUnavailable = errors.New("chain resources unavailable")
)

// Detail is a chain with its tokens and resources.
type Detail struct {
	Chain     *models.Chain
	Tokens    []models.Token
	Resources []models.ChainResource
}

// available loads the chain an account environment may use. A missing row and a
// nil chain are ErrChainNotFound, a chain of the other network kind is
// ErrChainNotInEnvironment, and any other failure is ErrChainLookup.
func (s *Service) available(ctx context.Context, id, environment string) (*models.Chain, error) {
	chain, err := s.FindForEnvironment(ctx, id, environment)
	switch {
	case errors.Is(err, ErrChainNotInEnvironment):
		return nil, err
	case err != nil && !errors.Is(err, models.ErrRepositoryNotFound):
		return nil, fmt.Errorf("%w: %w", ErrChainLookup, err)
	case chain == nil:
		return nil, ErrChainNotFound
	}
	return chain, nil
}

// DetailFor returns the chain with its tokens and resources. A failed token or
// resource read leaves that list nil instead of failing the chain.
func (s *Service) DetailFor(ctx context.Context, id, environment string) (Detail, error) {
	chain, err := s.available(ctx, id, environment)
	if err != nil {
		return Detail{}, err
	}

	tokens, err := s.FindTokens(ctx, id)
	if err != nil {
		slog.Warn("chains: load tokens for chain view", "chain", id, "error", err)
	}
	resources, err := s.FindResources(ctx, id)
	if err != nil {
		slog.Warn("chains: load resources for chain view", "chain", id, "error", err)
	}
	return Detail{Chain: chain, Tokens: tokens, Resources: resources}, nil
}

// TokensFor returns the tokens of a chain the account environment may use.
func (s *Service) TokensFor(ctx context.Context, id, environment string) ([]models.Token, error) {
	if _, err := s.available(ctx, id, environment); err != nil {
		return nil, err
	}
	tokens, err := s.FindTokens(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokensUnavailable, err)
	}
	return tokens, nil
}

// ResourcesFor returns the resources of a chain the account environment may use.
func (s *Service) ResourcesFor(ctx context.Context, id, environment string) ([]models.ChainResource, error) {
	if _, err := s.available(ctx, id, environment); err != nil {
		return nil, err
	}
	resources, err := s.FindResources(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrResourcesUnavailable, err)
	}
	return resources, nil
}
