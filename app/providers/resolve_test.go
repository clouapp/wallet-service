package providers

import (
	"testing"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/require"
)

type unloadedService struct{}

func TestResolve_RefusesATypedNilBinding(t *testing.T) {
	require.NotNil(t, foundation.App)
	var missing *unloadedService
	foundation.App.Singleton(missing, func(contractsfoundation.Application) (any, error) { return missing, nil })

	_, err := resolve[*unloadedService](foundation.App)

	require.Error(t, err)
}
