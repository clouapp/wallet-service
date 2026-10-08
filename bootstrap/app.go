package bootstrap

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractsconfiguration "github.com/goravel/framework/contracts/foundation/configuration"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/container"
	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/providers"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/database/seeders"
	"github.com/macrowallets/waas/routes"
)

// Boot wires Goravel and returns the application instance.
func Boot() contractsfoundation.Application {
	return foundation.Setup().
		WithMigrations(Migrations).
		WithProviders(Providers).
		WithSeeders(seeders.All).
		WithJobs(Jobs).
		WithCommands(Commands).
		WithRules(Rules).
		WithConfig(bootConfig).
		WithMiddleware(func(h contractsconfiguration.Middleware) {
			h.Use(middleware.GlobalChain(requestTimeout(), middleware.InboundSignatureDeps{
				Subscriptions: container.MustMake[*ingest.Subscriptions](),
				Lookup:        container.MustMake[*ingest.Catalog]().Lookup,
			})...).
				Recover(middleware.RecoverPanic)
		}).
		WithRouting(registerRoutes).
		Create()
}

// registerRoutes names the rate limiters before the routes that use them, then
// registers every route group.
func registerRoutes() {
	middleware.RegisterThrottles(appfacades.RateLimiter())
	routes.RegisterHTTP()
}

// bootConfig loads configuration and then installs the redacting log handler.
// WithConfig runs before service providers, which is the last moment a channel
// can be rewritten: the framework caches handlers on first use.
func bootConfig() {
	config.Boot()
	checkBootConfig()
	providers.InstallLogRedaction(appfacades.Config(), appfacades.App().Json())
}

// RequestTimeoutHandler is the hard cut for the local server: it answers 504 at
// http.request_timeout whatever the handler does. Lambda mode does not use it.
func RequestTimeoutHandler() func(http.Handler) http.Handler {
	return middleware.TimeoutHandler(requestTimeout())
}

// checkBootConfig refuses to start on a missing or malformed secret. APP_KEY
// and JWT_SECRET are exempt only for the commands that create them
// (`artisan key:generate`, `artisan jwt:secret`), which have to run on a fresh
// env file. A short JWT_SECRET is a warning.
func checkBootConfig() {
	cfg := appfacades.Config()
	if !config.IsKeyGenerationCommand(os.Args[1:]) {
		warnings, err := config.ValidateSecrets(cfg.GetString("app.key"), cfg.GetString("jwt.secret"))
		for _, warning := range warnings {
			slog.Warn(warning)
		}
		if err != nil {
			panic(fmt.Errorf("refusing to boot: %w", err))
		}
	}
	if _, err := middleware.ParseTrustedProxies(cfg.GetString("http.trusted_proxies")); err != nil {
		panic(fmt.Errorf("refusing to boot: TRUSTED_PROXIES: %w", err))
	}
}

// requestTimeout is http.request_timeout, the same key the gin driver used.
// Zero or a missing config leaves the chain without a deadline.
func requestTimeout() time.Duration {
	const defaultRequestTimeoutSeconds = 30
	seconds := defaultRequestTimeoutSeconds
	if cfg := appfacades.Config(); cfg != nil {
		seconds = cfg.GetInt("http.request_timeout", defaultRequestTimeoutSeconds)
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
