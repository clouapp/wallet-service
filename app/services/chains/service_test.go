package chains_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
)

func TestList_For_Environment(t *testing.T) {
	t.Parallel()

	active := []models.Chain{{ID: "eth"}, {ID: "btc"}}
	mainnet := []models.Chain{{ID: "eth"}}
	testnet := []models.Chain{{ID: "teth"}}
	storeErr := errors.New("store down")

	tests := []struct {
		name        string
		environment string
		store       *fakeCatalog
		want        []models.Chain
		wantErr     error
		wantActive  int
		wantTestnet int
		isTestnet   bool
	}{
		{
			name:        "prod lists mainnet",
			environment: models.EnvironmentProd,
			store:       &fakeCatalog{active: active, mainnet: mainnet, testnet: testnet},
			want:        mainnet,
			wantTestnet: 1,
			isTestnet:   false,
		},
		{
			name:        "test lists testnet",
			environment: models.EnvironmentTest,
			store:       &fakeCatalog{active: active, mainnet: mainnet, testnet: testnet},
			want:        testnet,
			wantTestnet: 1,
			isTestnet:   true,
		},
		{
			name:        "missing environment lists every active chain",
			environment: "",
			store:       &fakeCatalog{active: active, mainnet: mainnet, testnet: testnet},
			want:        active,
			wantActive:  1,
		},
		{
			name:        "other environment lists every active chain",
			environment: "sandbox",
			store:       &fakeCatalog{active: active, mainnet: mainnet, testnet: testnet},
			want:        active,
			wantActive:  1,
		},
		{
			name:        "store error is returned",
			environment: models.EnvironmentProd,
			store:       &fakeCatalog{err: storeErr},
			wantErr:     storeErr,
			wantTestnet: 1,
			isTestnet:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := chainsvc.NewService(chainsvc.Deps{Chains: test.store}).ListForEnvironment(context.Background(), test.environment)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.want, got)
			}
			require.Equal(t, test.wantActive, test.store.activeCalls)
			require.Equal(t, test.wantTestnet, test.store.testnetCalls)
			if test.wantTestnet > 0 {
				require.Equal(t, test.isTestnet, test.store.lastTestnet)
			}
		})
	}
}

func TestFind_By_IDTokensAndResources(t *testing.T) {
	t.Parallel()

	store := &fakeCatalog{active: []models.Chain{{ID: "eth"}}}
	tokens := &fakeTokens{rows: []models.Token{{ChainID: "eth"}}}
	resources := &fakeResources{rows: []models.ChainResource{{ChainID: "eth"}}}
	svc := chainsvc.NewService(chainsvc.Deps{Chains: store, Tokens: tokens, Resources: resources})

	chain, err := svc.FindByID(context.Background(), "eth")
	require.NoError(t, err)
	require.Equal(t, "eth", chain.ID)

	missing, err := svc.FindByID(context.Background(), "nope")
	require.NoError(t, err)
	require.Nil(t, missing)

	gotTokens, err := svc.FindTokens(context.Background(), "eth")
	require.NoError(t, err)
	require.Equal(t, tokens.rows, gotTokens)

	gotResources, err := svc.FindResources(context.Background(), "eth")
	require.NoError(t, err)
	require.Equal(t, resources.rows, gotResources)

	_, err = svc.FindTokens(nil, "eth")
	require.EqualError(t, err, "list chain tokens: context is required")
	_, err = chainsvc.NewService(chainsvc.Deps{Chains: store}).FindTokens(context.Background(), "eth")
	require.EqualError(t, err, "chains service: tokens repository is required")
	_, err = chainsvc.NewService(chainsvc.Deps{Chains: store}).FindResources(context.Background(), "eth")
	require.EqualError(t, err, "chains service: chain resources repository is required")
}

func TestList_For_EnvironmentRequiresContextAndCatalog(t *testing.T) {
	t.Parallel()

	_, err := chainsvc.NewService(chainsvc.Deps{Chains: &fakeCatalog{}}).ListForEnvironment(nil, models.EnvironmentProd)
	require.EqualError(t, err, "list chains: context is required")

	_, err = chainsvc.NewService(chainsvc.Deps{}).ListForEnvironment(context.Background(), models.EnvironmentProd)
	require.EqualError(t, err, "chains service: chains repository is required")

	var service *chainsvc.Service
	_, err = service.ListForEnvironment(context.Background(), "")
	require.EqualError(t, err, "chains service: chains repository is required")
}

type fakeCatalog struct {
	active       []models.Chain
	mainnet      []models.Chain
	testnet      []models.Chain
	err          error
	activeCalls  int
	testnetCalls int
	lastTestnet  bool
}

func (f *fakeCatalog) FindActive(context.Context) ([]models.Chain, error) {
	f.activeCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.active, nil
}

func (f *fakeCatalog) FindByID(_ context.Context, id string) (*models.Chain, error) {
	if f.err != nil {
		return nil, f.err
	}
	for i := range f.active {
		if f.active[i].ID == id {
			return &f.active[i], nil
		}
	}
	return nil, nil
}

func (f *fakeCatalog) FindByTestnet(_ context.Context, isTestnet bool) ([]models.Chain, error) {
	f.testnetCalls++
	f.lastTestnet = isTestnet
	if f.err != nil {
		return nil, f.err
	}
	if isTestnet {
		return f.testnet, nil
	}
	return f.mainnet, nil
}

type fakeTokens struct {
	rows []models.Token
}

func (f *fakeTokens) FindByChainID(context.Context, string) ([]models.Token, error) {
	return f.rows, nil
}

type fakeResources struct {
	rows []models.ChainResource
}

func (f *fakeResources) FindByChainID(context.Context, string) ([]models.ChainResource, error) {
	return f.rows, nil
}
