package providers

import (
	"testing"

	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/repositories"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	currencysvc "github.com/macrowallets/waas/app/services/currencies"
	"github.com/stretchr/testify/assert"
)

func TestChain_Provider_RegistersTheChainGraph(t *testing.T) {
	require.NotNil(t, foundation.App)
	(&ChainServiceProvider{}).Register(foundation.App)

	chains, err := container.Make[*repositories.ChainRepository]()
	require.NoError(t, err)
	require.NotNil(t, chains)

	tokens, err := container.Make[*repositories.TokenRepository]()
	require.NoError(t, err)
	assert.NotNil(t, tokens)

	resources, err := container.Make[*repositories.ChainResourceRepository]()
	require.NoError(t, err)
	assert.NotNil(t, resources)

	currencies, err := container.Make[*repositories.CurrencyRepository]()
	require.NoError(t, err)
	require.NotNil(t, currencies)

	assert.Same(t, chains, container.MustMake[*repositories.ChainRepository]())
	assert.Same(t, currencies, container.MustMake[*repositories.CurrencyRepository]())

	catalogue, err := container.Make[*chainsvc.Service]()
	require.NoError(t, err)
	require.NotNil(t, catalogue)
	assert.Same(t, catalogue, container.MustMake[*chainsvc.Service]())

	currencyCatalogue, err := container.Make[*currencysvc.Service]()
	require.NoError(t, err)
	require.NotNil(t, currencyCatalogue)
	assert.Same(t, currencyCatalogue, container.MustMake[*currencysvc.Service]())
}
