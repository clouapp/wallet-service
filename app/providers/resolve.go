package providers

import (
	"fmt"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/container"
)

// resolve makes T from the container and asserts its type, so a binding that
// returns the wrong thing fails at boot with the type named.
func resolve[T any](app foundation.Application) (T, error) {
	var zero T
	instance, err := app.Make(zero)
	if err != nil {
		return zero, err
	}
	if container.IsNil(instance) {
		return zero, fmt.Errorf("container returned nil, want %T", zero)
	}
	typed, ok := instance.(T)
	if !ok {
		return zero, fmt.Errorf("container returned %T, want %T", instance, zero)
	}
	return typed, nil
}
