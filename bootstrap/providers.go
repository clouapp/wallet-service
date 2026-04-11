package bootstrap

import (
	frameworkauth "github.com/goravel/framework/auth"
	frameworkcache "github.com/goravel/framework/cache"
	"github.com/goravel/framework/contracts/foundation"
	frameworkcrypt "github.com/goravel/framework/crypt"
	frameworkdatabase "github.com/goravel/framework/database"
	frameworkhttp "github.com/goravel/framework/http"
	frameworklog "github.com/goravel/framework/log"
	frameworkmail "github.com/goravel/framework/mail"
	frameworkqueue "github.com/goravel/framework/queue"
	frameworkroute "github.com/goravel/framework/route"
	frameworktesting "github.com/goravel/framework/testing"
	frameworkvalidation "github.com/goravel/framework/validation"
	frameworkview "github.com/goravel/framework/view"
	gin "github.com/goravel/gin"
	goravel_postgres "github.com/goravel/postgres"
	goravel_redis "github.com/goravel/redis"

	"github.com/macrowallets/waas/app/providers"
)

func Providers() []foundation.ServiceProvider {
	return []foundation.ServiceProvider{
		&frameworklog.ServiceProvider{},
		&goravel_postgres.ServiceProvider{},
		&frameworkdatabase.ServiceProvider{},
		&frameworkhttp.ServiceProvider{},
		&goravel_redis.ServiceProvider{},
		&frameworkcache.ServiceProvider{},
		&frameworkvalidation.ServiceProvider{},
		&frameworkview.ServiceProvider{},
		&gin.ServiceProvider{},
		&frameworkroute.ServiceProvider{},
		&frameworktesting.ServiceProvider{},
		&frameworkauth.ServiceProvider{},
		&frameworkqueue.ServiceProvider{},
		&frameworkmail.ServiceProvider{},
		&frameworkcrypt.ServiceProvider{},
		&providers.MigrationsServiceProvider{},
		&providers.AuthServiceProvider{},
		&providers.AppServiceProvider{},
		&providers.RouteServiceProvider{},
	}
}
