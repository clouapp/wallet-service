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
	typedNilService  struct{}
)

func bind[T any](t *testing.T, value any) {
	t.Helper()
	var zero T
	foundation.App.Singleton(zero, func(contractsfoundation.Application) (any, error) {
		return value, nil
	})
}

func TestMust_Make_ResolvesTheBindingRegisteredUnderItsType(t *testing.T) {
	want := &boundService{name: "accounts"}
	bind[*boundService](t, want)

	assert.Same(t, want, MustMake[*boundService]())
}

func TestMust_Make_ResolvesEachTypeIndependently(t *testing.T) {
	first := &pairedServiceA{name: "users"}
	second := &pairedServiceB{name: "accounts"}
	bind[*pairedServiceA](t, first)
	bind[*pairedServiceB](t, second)

	assert.Same(t, first, MustMake[*pairedServiceA]())
	assert.Same(t, second, MustMake[*pairedServiceB]())
}

func TestMake_Unbound_TypeIsAnErrorNamingTheType(t *testing.T) {
	got, err := Make[*unboundService]()

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "container: resolving *container.unboundService")
}

func TestMake_Wrong_TypeIsAnErrorNamingBoth(t *testing.T) {
	bind[*misboundService](t, &misboundActual{})

	_, err := Make[*misboundService]()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "*container.misboundService")
	assert.Contains(t, err.Error(), "*container.misboundActual")
}

func TestMake_A_BindingThatAnswersNilIsNotAResolution(t *testing.T) {
	bind[*wrongTypeService](t, nil)

	_, err := Make[*wrongTypeService]()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "*container.wrongTypeService")
}

func TestMake_A_TypedNilBindingIsNotAResolution(t *testing.T) {
	var nilService *typedNilService
	bind[*typedNilService](t, nilService)

	got, err := Make[*typedNilService]()

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "*container.typedNilService")
}

func TestIs_Nil_SeesThroughATypedNil(t *testing.T) {
	var (
		pointer *boundService
		slice   []string
		mapping map[string]int
		fn      func()
		iface   error
	)
	for name, value := range map[string]any{
		"untyped nil": nil, "nil pointer": pointer, "nil slice": slice,
		"nil map": mapping, "nil func": fn, "nil interface": iface,
	} {
		assert.True(t, IsNil(value), name)
	}
	for name, value := range map[string]any{
		"pointer": &boundService{}, "struct": boundService{}, "int": 0, "empty slice": []string{},
	} {
		assert.False(t, IsNil(value), name)
	}
}

func TestMake_With_NoApplicationIsAnErrorNotAPanic(t *testing.T) {
	previous := foundation.App
	foundation.App = nil
	t.Cleanup(func() { foundation.App = previous })

	_, err := Make[*appMissingSvc]()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no application is booted")
}

func TestMust_Make_PanicsOnAMiss(t *testing.T) {
	_, err := Make[*mustMissingSvc]()
	require.Error(t, err)

	assert.PanicsWithError(t, err.Error(), func() { MustMake[*mustMissingSvc]() })
}
