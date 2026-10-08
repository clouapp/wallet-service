package testutil

import (
	"fmt"

	frameworkauth "github.com/goravel/framework/auth"
	frameworkcache "github.com/goravel/framework/cache"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	frameworkcrypt "github.com/goravel/framework/crypt"
	frameworkdatabase "github.com/goravel/framework/database"
	frameworkevent "github.com/goravel/framework/event"
	frameworkfoundation "github.com/goravel/framework/foundation"
	frameworkhash "github.com/goravel/framework/hash"
	frameworkhttp "github.com/goravel/framework/http"
	frameworklog "github.com/goravel/framework/log"
	frameworkmail "github.com/goravel/framework/mail"
	frameworkqueue "github.com/goravel/framework/queue"
	frameworkroute "github.com/goravel/framework/route"
	frameworktesting "github.com/goravel/framework/testing"
	frameworkvalidation "github.com/goravel/framework/validation"
	frameworkview "github.com/goravel/framework/view"
	gin "github.com/goravel/gin"
	goravelpostgres "github.com/goravel/postgres"
	goravelredis "github.com/goravel/redis"

	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

// BootTest initializes Goravel for testing (service-level tests, no routes).
// Config is set before Boot so ORM and other providers initialise correctly.
func BootTest() contractsfoundation.Application {
	if err := testenv.Load(); err != nil {
		panic(fmt.Sprintf("load isolated testing environment: %v", err))
	}
	return frameworkfoundation.Setup().
		WithMigrations(bootstrap.Migrations).
		WithProviders(testProviders).
		WithConfig(config.Boot).
		Create()
}

func testProviders() []contractsfoundation.ServiceProvider {
	return []contractsfoundation.ServiceProvider{
		&frameworklog.ServiceProvider{},
		&goravelpostgres.ServiceProvider{},
		&frameworkdatabase.ServiceProvider{},
		&frameworkhttp.ServiceProvider{},
		&goravelredis.ServiceProvider{},
		&frameworkcache.ServiceProvider{},
		&frameworkvalidation.ServiceProvider{},
		&frameworkview.ServiceProvider{},
		&gin.ServiceProvider{},
		&frameworkroute.ServiceProvider{},
		&frameworktesting.ServiceProvider{},
		&frameworkauth.ServiceProvider{},
		&frameworkqueue.ServiceProvider{},
		&frameworkevent.ServiceProvider{},
		&frameworkmail.ServiceProvider{},
		&frameworkcrypt.ServiceProvider{},
		&frameworkhash.ServiceProvider{},
	}
}
