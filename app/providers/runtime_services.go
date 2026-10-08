package providers

import (
	"fmt"

	"github.com/goravel/framework/contracts/foundation"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/ingest"
)

// registerRuntimeServices binds the services that buildVaultContainer still
// owns, so routes, jobs and commands can MustMake them.
func registerRuntimeServices(app foundation.Application) {
	bindRuntime(app, func(c *container.Container) *deposit.Service { return c.DepositService }, "deposit service")
	bindRuntime(app, func(c *container.Container) *ingest.Service { return c.IngestService }, "ingest service")
}

func bindRuntime[T any](app foundation.Application, load func(*container.Container) T, name string) {
	var key T
	app.Singleton(key, func(foundation.Application) (any, error) {
		return initialized(load(container.Get()), name)
	})
}

// initialized refuses a service the container left nil, a typed nil pointer
// included, so the boot fails instead of the first request.
func initialized[T any](value T, name string) (T, error) {
	if container.IsNil(value) {
		var zero T
		return zero, fmt.Errorf("vault: %s is not initialized", name)
	}
	return value, nil
}
