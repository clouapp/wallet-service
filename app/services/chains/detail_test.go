package chains_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

type erroringTokens struct{}

func (erroringTokens) FindByChainID(context.Context, string) ([]models.Token, error) {
	return nil, errors.New("tokens store down")
}

type erroringResources struct{}

func (erroringResources) FindByChainID(context.Context, string) ([]models.ChainResource, error) {
	return nil, errors.New("resources store down")
}

func TestDetail_For_ChainLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		catalog     *fakeCatalog
		environment string
		wantErr     error
	}{
		{"missing row is not found", &fakeCatalog{err: fmt.Errorf("find chain: %w", models.ErrRepositoryNotFound)}, "", chainsvc.ErrChainNotFound},
		{"nil chain is not found", &fakeCatalog{}, "", chainsvc.ErrChainNotFound},
		{"a store failure is a lookup failure", &fakeCatalog{err: errors.New("connection refused")}, "", chainsvc.ErrChainLookup},
		{"the other network kind is refused", &fakeCatalog{active: []models.Chain{{ID: "eth", IsTestnet: true}}}, models.EnvironmentProd, chainsvc.ErrChainNotInEnvironment},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := chainsvc.NewService(chainsvc.Deps{Chains: tc.catalog, Tokens: &fakeTokens{}, Resources: &fakeResources{}})

			_, detailErr := svc.DetailFor(context.Background(), "eth", tc.environment)
			_, tokensErr := svc.TokensFor(context.Background(), "eth", tc.environment)
			_, resourcesErr := svc.ResourcesFor(context.Background(), "eth", tc.environment)

			for name, err := range map[string]error{"DetailFor": detailErr, "TokensFor": tokensErr, "ResourcesFor": resourcesErr} {
				require.ErrorIs(t, err, tc.wantErr, name)
			}
		})
	}
}

func TestDetail_For_KeepsTheLookupCauseOutOfTheSentinelText(t *testing.T) {
	t.Parallel()

	cause := errors.New("connection refused")
	svc := chainsvc.NewService(chainsvc.Deps{Chains: &fakeCatalog{err: cause}})

	_, err := svc.DetailFor(context.Background(), "eth", "")

	require.ErrorIs(t, err, cause)
	require.ErrorIs(t, err, chainsvc.ErrChainLookup)
}

func TestDetail_For_ServesTheChainWhenTheSideReadsFail(t *testing.T) {
	t.Parallel()

	svc := chainsvc.NewService(chainsvc.Deps{
		Chains:    &fakeCatalog{active: []models.Chain{{ID: "eth"}}},
		Tokens:    erroringTokens{},
		Resources: erroringResources{},
	})

	detail, err := svc.DetailFor(context.Background(), "eth", "")

	require.NoError(t, err)
	assert.Equal(t, "eth", detail.Chain.ID)
	assert.Nil(t, detail.Tokens)
	assert.Nil(t, detail.Resources)
}

func TestDetail_For_ReturnsTheChainTokensAndResources(t *testing.T) {
	t.Parallel()

	tokens := []models.Token{{ChainID: "eth", Symbol: "USDC"}}
	resources := []models.ChainResource{{ChainID: "eth"}}
	svc := chainsvc.NewService(chainsvc.Deps{
		Chains:    &fakeCatalog{active: []models.Chain{{ID: "eth"}}},
		Tokens:    &fakeTokens{rows: tokens},
		Resources: &fakeResources{rows: resources},
	})

	detail, err := svc.DetailFor(context.Background(), "eth", "")

	require.NoError(t, err)
	assert.Equal(t, tokens, detail.Tokens)
	assert.Equal(t, resources, detail.Resources)
}

func TestTokens_For_And_ResourcesFor_FailWhenTheirReadFails(t *testing.T) {
	t.Parallel()

	svc := chainsvc.NewService(chainsvc.Deps{
		Chains:    &fakeCatalog{active: []models.Chain{{ID: "eth"}}},
		Tokens:    erroringTokens{},
		Resources: erroringResources{},
	})

	_, tokensErr := svc.TokensFor(context.Background(), "eth", "")
	_, resourcesErr := svc.ResourcesFor(context.Background(), "eth", "")

	require.ErrorIs(t, tokensErr, chainsvc.ErrTokensUnavailable)
	require.ErrorIs(t, resourcesErr, chainsvc.ErrResourcesUnavailable)
}
