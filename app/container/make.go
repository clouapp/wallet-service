// Package container resolves a binding out of Goravel's service container by
// the type that was bound, so a consumer can name a dependency without naming
// the provider that built it.
//
// app/providers is the composition root: it registers each singleton under a
// typed nil. Routes ask for that type with MustMake. A binding is never keyed
// by string.
package container

import (
	"fmt"

	"github.com/goravel/framework/facades"
	"github.com/goravel/framework/foundation"
)

// Make resolves T from the container and asserts its type. A miss and a
// wrong-typed binding are both errors, and both name T.
func Make[T any]() (T, error) {
	var zero T
	if foundation.App == nil {
		return zero, fmt.Errorf("container: resolving %T: no application is booted", zero)
	}
	instance, err := facades.App().Make(zero)
	if err != nil {
		return zero, fmt.Errorf("container: resolving %T: %w", zero, err)
	}
	if instance == nil {
		return zero, fmt.Errorf("container: resolving %T: binding returned nil", zero)
	}
	typed, ok := instance.(T)
	if !ok {
		return zero, fmt.Errorf("container: %T is bound as %T", zero, instance)
	}
	return typed, nil
}

// MustMake is Make for boot-time wiring, where a miss is a wiring bug.
// It panics with Make's error so the process fails at boot instead of on a
// live request.
func MustMake[T any]() T {
	typed, err := Make[T]()
	if err != nil {
		panic(err)
	}
	return typed
}
