package bootstrap

import (
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/database/seeders"
	"github.com/macrowallets/waas/routes"
)

// Boot wires Goravel (providers must be registered via WithProviders so route/http bindings exist before routes load).
func Boot() {
	_ = foundation.Setup().
		WithProviders(config.AppProviders).
		WithSeeders(seeders.All).
		WithRouting(func() {
			routes.Api()
		}).
		WithConfig(func() {
			config.Boot(foundation.App)
		}).
		Create()
}
