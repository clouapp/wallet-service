package bootstrap_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/container"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
)

// The providers key every service by its type (a typed nil pointer); the
// framework keys its own bindings by string. Every typed binding must build,
// hold the type it is keyed by, and come back as the same instance: a
// singleton built twice would split the state of the services that share it.
func TestComposition_Root_ResolvesEveryTypedBindingToOneInstance(t *testing.T) {
	var keys []any
	for _, key := range app.Bindings() {
		if typed := reflect.TypeOf(key); typed != nil && typed.Kind() == reflect.Pointer && reflect.ValueOf(key).IsNil() {
			keys = append(keys, key)
		}
	}
	require.NotEmpty(t, keys, "no typed binding is registered")

	for _, key := range keys {
		t.Run(fmt.Sprintf("%T", key), func(t *testing.T) {
			first, err := app.Make(key)
			require.NoError(t, err)
			require.False(t, container.IsNil(first), "the binding returned nil")
			assert.Equal(t, reflect.TypeOf(key), reflect.TypeOf(first), "the binding returned another type")

			second, err := app.Make(key)
			require.NoError(t, err)
			assert.True(t, first == second, "a second resolution built another instance")
		})
	}
}

// The registry the registry service fills is the one every consumer gets.
func TestComposition_Root_SharesTheChainRegistry(t *testing.T) {
	registry := container.MustMake[*chainpkg.Registry]()
	assert.Same(t, container.MustMake[*chainregistry.ChainRegistryService]().Registry(), registry)
}
