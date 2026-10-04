package container

import (
	"testing"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/foundation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// There is one application per process, so each test brings its own stand-in
// type, which is also the key it binds under.
type (
	boundService     struct{ name string }
	pairedServiceA   struct{ name string }
	pairedServiceB   struct{ name string }
	unboundService   struct{}
	misboundService  struct{}
	misboundActual   struct{}
	appMissingSvc    struct{}
	mustMissingSvc   struct{}
	wrongTypeService struct{}
)

func bind[T any](t *testing.T, value any) {
	t.Helper()
	var zero T
	foundation.App.Singleton(zero, func(contractsfoundation.Application) (any, error) {
		return value, nil
	})
}

func TestMustMake_ResolvesTheBindingRegisteredUnderItsType(t *testing.T) {
	want := &boundService{name: "accounts"}
	bind[*boundService](t, want)

	assert.Same(t, want, MustMake[*boundService]())
}

func TestMustMake_ResolvesEachTypeIndependently(t *testing.T) {
	first := &pairedServiceA{name: "users"}
	second := &pairedServiceB{name: "accounts"}
	bind[*pairedServiceA](t, first)
	bind[*pairedServiceB](t, second)

	assert.Same(t, first, MustMake[*pairedServiceA]())
	assert.Same(t, second, MustMake[*pairedServiceB]())
}

func TestMake_UnboundTypeIsAnErrorNamingTheType(t *testing.T) {
	got, err := Make[*unboundService]()

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "container: resolving *container.unboundService")
}

func TestMake_WrongTypeIsAnErrorNamingBoth(t *testing.T) {
	bind[*misboundService](t, &misboundActual{})

	_, err := Make[*misboundService]()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "*container.misboundService")
	assert.Contains(t, err.Error(), "*container.misboundActual")
}

func TestMake_ABindingThatAnswersNilIsNotAResolution(t *testing.T) {
	bind[*wrongTypeService](t, nil)

	_, err := Make[*wrongTypeService]()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "*container.wrongTypeService")
}

func TestMake_WithNoApplicationIsAnErrorNotAPanic(t *testing.T) {
	previous := foundation.App
	foundation.App = nil
	t.Cleanup(func() { foundation.App = previous })

	_, err := Make[*appMissingSvc]()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no application is booted")
}

func TestMustMake_PanicsOnAMiss(t *testing.T) {
	_, err := Make[*mustMissingSvc]()
	require.Error(t, err)

	assert.PanicsWithError(t, err.Error(), func() { MustMake[*mustMissingSvc]() })
}
