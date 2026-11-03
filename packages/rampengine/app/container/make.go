package container

import (
	"fmt"
	"reflect"

	"github.com/goravel/framework/foundation"
)

// Make resolves key and asserts its type in one place. Goravel's Make answers
// (any, error), so every call site would otherwise carry an error check and a
// bare assertion that panics without naming the binding it was asked for.
//
// Use it where the caller can still do something with the failure -- a factory
// resolved per request, a dispatcher resolved per job. Boot-time wiring wants
// MustMake.
func Make[T any](key any) (T, error) {
	var zero T
	// Not facades.App(): app/facades imports this package.
	instance, err := foundation.App.Make(key)
	if err != nil {
		return zero, fmt.Errorf("container: resolving %v: %w", key, err)
	}
	typed, ok := instance.(T)
	if !ok {
		return zero, fmt.Errorf("container: %v is %T, want %v", key, instance, reflect.TypeFor[T]())
	}
	return typed, nil
}

// MustMake is Make for boot-time wiring, where a miss is a wiring bug rather
// than a condition to handle -- the bargain template.Must makes. Failing at
// boot is what stops the same miss from becoming a 500 on a live route.
func MustMake[T any](key any) T {
	typed, err := Make[T](key)
	if err != nil {
		panic(err)
	}
	return typed
}
