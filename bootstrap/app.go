package bootstrap

import (
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/database/seeders"
)

// Boot wires Goravel and returns the application instance.
func Boot() contractsfoundation.Application {
	return foundation.Setup().
		WithMigrations(Migrations).
		WithProviders(Providers).
		WithSeeders(seeders.All).
		WithConfig(config.Boot).
		Create()
}
