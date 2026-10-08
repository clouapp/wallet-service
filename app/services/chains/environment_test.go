package chains_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

func TestFind_ForEnvironment(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{active: []models.Chain{{ID: "eth"}, {ID: "sepolia", IsTestnet: true}}}
	service := chainsvc.NewService(chainsvc.Deps{Chains: catalog})

	tests := []struct {
		name        string
		chainID     string
		environment string
		wantErr     error
		wantChain   bool
	}{
		{"prod sees a mainnet chain", "eth", models.EnvironmentProd, nil, true},
		{"prod does not see a testnet chain", "sepolia", models.EnvironmentProd, chainsvc.ErrChainNotInEnvironment, true},
		{"test sees a testnet chain", "sepolia", models.EnvironmentTest, nil, true},
		{"test does not see a mainnet chain", "eth", models.EnvironmentTest, chainsvc.ErrChainNotInEnvironment, true},
		{"a missing environment sees every chain", "sepolia", "", nil, true},
		{"another environment sees every chain", "eth", "sandbox", nil, true},
		{"an unknown chain is not filtered", "nope", models.EnvironmentProd, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			chain, err := service.FindForEnvironment(context.Background(), tc.chainID, tc.environment)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantChain, chain != nil)
		})
	}
}

func TestFind_ForEnvironmentReturnsTheStoreError(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("store down")
	service := chainsvc.NewService(chainsvc.Deps{Chains: &fakeCatalog{err: storeErr}})

	chain, err := service.FindForEnvironment(context.Background(), "eth", models.EnvironmentProd)
	require.ErrorIs(t, err, storeErr)
	assert.NotErrorIs(t, err, chainsvc.ErrChainNotInEnvironment)
	assert.Nil(t, chain)
}
