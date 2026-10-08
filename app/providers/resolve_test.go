package providers

import (
	"testing"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unloadedService struct{}

func TestInitialized_RefusesATypedNilService(t *testing.T) {
	var missing *unloadedService

	got, err := initialized(missing, "unloaded service")

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "unloaded service is not initialized")
}

func TestInitialized_KeepsABuiltService(t *testing.T) {
	built := &unloadedService{}

	got, err := initialized(built, "unloaded service")

	require.NoError(t, err)
	assert.Same(t, built, got)
}

func TestResolve_RefusesATypedNilBinding(t *testing.T) {
	require.NotNil(t, foundation.App)
	var missing *unloadedService
	foundation.App.Singleton(missing, func(contractsfoundation.Application) (any, error) { return missing, nil })

	_, err := resolve[*unloadedService](foundation.App)

	require.Error(t, err)
}
