package providers

import (
	"testing"

	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
)

func TestChainProvider_RegistersTheChainGraph(t *testing.T) {
	require.NotNil(t, foundation.App)
	(&ChainServiceProvider{}).Register(foundation.App)

	chains, err := container.Make[*repositories.ChainRepository]()
	require.NoError(t, err)
	require.NotNil(t, chains)

	tokens, err := container.Make[*repositories.TokenRepository]()
	require.NoError(t, err)
	require.NotNil(t, tokens)

	resources, err := container.Make[*repositories.ChainResourceRepository]()
	require.NoError(t, err)
	require.NotNil(t, resources)

	currencies, err := container.Make[*repositories.CurrencyRepository]()
	require.NoError(t, err)
	require.NotNil(t, currencies)

	require.Same(t, chains, container.MustMake[*repositories.ChainRepository]())
	require.Same(t, currencies, container.MustMake[*repositories.CurrencyRepository]())
}
