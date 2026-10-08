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
	"reflect"

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
	if IsNil(instance) {
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

// IsNil reports whether value is nil or an interface holding a nil pointer,
// map, slice, func or channel. value == nil is false for a typed nil, so a
// service that failed to build would pass a plain nil check and fail on the
// first request instead of at boot.
func IsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
