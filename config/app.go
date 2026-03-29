package config

import (
	"os"
	"strconv"

	contractscache "github.com/goravel/framework/contracts/cache"
	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractsroute "github.com/goravel/framework/contracts/route"
	frameworkauth "github.com/goravel/framework/auth"
	frameworkcache "github.com/goravel/framework/cache"
	frameworkcrypt "github.com/goravel/framework/crypt"
	frameworkdatabase "github.com/goravel/framework/database"
	"github.com/goravel/framework/facades"
	frameworkhttp "github.com/goravel/framework/http"
	frameworklog "github.com/goravel/framework/log"
	frameworkmail "github.com/goravel/framework/mail"
	frameworkqueue "github.com/goravel/framework/queue"
	frameworkroute "github.com/goravel/framework/route"
	frameworktesting "github.com/goravel/framework/testing"
	frameworkvalidation "github.com/goravel/framework/validation"
	frameworkview "github.com/goravel/framework/view"
	gin "github.com/goravel/gin"
	ginfacades "github.com/goravel/gin/facades"
	goravel_postgres "github.com/goravel/postgres"
	postgres_facades "github.com/goravel/postgres/facades"
	goravel_redis "github.com/goravel/redis"
	redisfacades "github.com/goravel/redis/facades"
	"github.com/macrowallets/waas/app/models"
	vaultproviders "github.com/macrowallets/waas/app/providers"
)

// AppProviders returns framework + app service providers. Used by bootstrap.WithProviders
// and by the app.providers config entry (Goravel loads bindings from WithProviders during Build).
func AppProviders() []contractsfoundation.ServiceProvider {
	return []contractsfoundation.ServiceProvider{
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
		&vaultproviders.MigrationsServiceProvider{},
		&vaultproviders.AuthServiceProvider{},
	}
}

// Boot initializes all configurations
func Boot(app contractsfoundation.Application) {
	facades.Config().Add("database", map[string]any{
		// Default database connection
		"default": env("DB_CONNECTION", "postgres"),

		// Database connections — Goravel v1.17 format with driver via
		"connections": map[string]any{
			"postgres": map[string]any{
				"driver":   "postgres",
				"host":     env("DB_HOST", "127.0.0.1"),
				"port":     envInt("DB_PORT", 5432),
				"database": env("DB_DATABASE", "vault"),
				"username": env("DB_USERNAME", "postgres"),
				"password": env("DB_PASSWORD", ""),
				"sslmode":  env("DB_SSLMODE", "disable"),
				"charset":  "utf8",
				"prefix":   "",
				"via": func() (contractsdriver.Driver, error) {
					return postgres_facades.Postgres("postgres")
				},
			},
		},

		// Redis connections (used by goravel/redis for cache/session)
		"redis": map[string]any{
			"default": map[string]any{
				"host":     env("REDIS_HOST", "127.0.0.1"),
				"port":     env("REDIS_PORT", "6379"),
				"password": env("REDIS_PASSWORD", ""),
				"database": envInt("REDIS_DB", 0),
			},
		},

		// Migration configuration
		"migrations": map[string]any{
			"table": "migrations",
		},

		// Connection pool settings
		"pool": map[string]any{
			"max_idle_conns": envInt("DB_MAX_IDLE_CONNS", 10),
			"max_open_conns": envInt("DB_MAX_OPEN_CONNS", 100),
			"max_lifetime":   envInt("DB_MAX_LIFETIME", 3600),
		},
	})

	facades.Config().Add("app", map[string]any{
		"name":     env("APP_NAME", "Macro Wallets"),
		"env":      env("APP_ENV", "local"),
		"debug":    envBool("APP_DEBUG", false),
		"timezone": env("APP_TIMEZONE", "UTC"),
		"locale":   env("APP_LOCALE", "en"),
		"key":       env("APP_KEY", ""),
		"url":       env("APP_URL", "http://localhost"),
		"providers": AppProviders(),
	})

	// Auth guards
	facades.Config().Add("auth", map[string]any{
		"defaults": map[string]any{"guard": "web"},
		"guards": map[string]any{
			"web": map[string]any{
				"driver":   "jwt",
				"provider": "users",
			},
		},
		"providers": map[string]any{
			"users": map[string]any{
				"driver": "orm",
				"model":  models.User{},
			},
		},
	})

	// Cache — use Redis via custom driver
	facades.Config().Add("cache", map[string]any{
		"default": env("CACHE_DRIVER", "redis"),
		"stores": map[string]any{
			"redis": map[string]any{
				"driver": "custom",
				"via": func() (contractscache.Driver, error) {
					return redisfacades.Cache("redis")
				},
			},
			"memory": map[string]any{
				"driver": "memory",
			},
		},
	})

	facades.Config().Add("jwt", map[string]any{
		"secret":      env("JWT_SECRET", ""),
		"ttl":         envInt("JWT_TTL", 15),
		"refresh_ttl": envInt("JWT_REFRESH_TTL", 43200),
	})

	// Mail
	facades.Config().Add("mail", map[string]any{
		"default": "smtp",
		"mailers": map[string]any{
			"smtp": map[string]any{
				"transport":  "smtp",
				"host":       env("MAIL_HOST", ""),
				"port":       envInt("MAIL_PORT", 587),
				"encryption": env("MAIL_ENCRYPTION", "tls"),
				"username":   env("MAIL_USERNAME", ""),
				"password":   env("MAIL_PASSWORD", ""),
			},
		},
		"from": map[string]any{
			"address": env("MAIL_FROM_ADDRESS", "noreply@vault.dev"),
			"name":    env("MAIL_FROM_NAME", "Vault"),
		},
	})

	facades.Config().Add("queue", map[string]any{
		"default": env("QUEUE_CONNECTION", "sync"),
		"connections": map[string]any{
			"sync": map[string]any{
				"driver": "sync",
			},
		},
		"failed": map[string]any{
			"database": "postgres",
			"table":    "failed_jobs",
		},
	})

	facades.Config().Add("http", map[string]any{
		"default":         "gin",
		"request_timeout": envInt("HTTP_REQUEST_TIMEOUT", 30),
		"drivers": map[string]any{
			"gin": map[string]any{
				"route": func() (contractsroute.Route, error) {
					return ginfacades.Route("gin"), nil
				},
			},
		},
	})
}

// Helper functions for environment variables
func env(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func envInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func envBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1"
	}
	return defaultValue
}
