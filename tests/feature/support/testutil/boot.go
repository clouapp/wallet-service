package testutil

import (
	"fmt"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	frameworkfoundation "github.com/goravel/framework/foundation"
	frameworktesting "github.com/goravel/framework/testing"

	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

// BootTest initializes Goravel for service-level tests: the providers the
// binary registers (bootstrap.Providers), without routes, commands or jobs.
// Config is set before Boot so ORM and other providers initialise correctly.
func BootTest() contractsfoundation.Application {
	if err := testenv.Load(); err != nil {
		panic(fmt.Sprintf("load isolated testing environment: %v", err))
	}
	return frameworkfoundation.Setup().
		WithMigrations(bootstrap.Migrations).
		WithProviders(bootstrap.Providers).
		WithConfig(config.Boot).
		Create()
}

// BootApp boots the application the binary boots (bootstrap.Boot) and then
// registers the framework testing provider, the one test-only override: the
// HTTP test case reads the router and the JSON codec from it. Production does
// not register it because its Boot asks for a session manager this service
// does not run. Call it after testenv.Load.
func BootApp() contractsfoundation.Application {
	app := bootstrap.Boot()
	testing := &frameworktesting.ServiceProvider{}
	testing.Register(app)
	testing.Boot(app)
	return app
}
