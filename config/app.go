package config

import (
	"os"
	"strconv"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractsroute "github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"
	frameworkhttp "github.com/goravel/framework/http"
	frameworklog "github.com/goravel/framework/log"
	frameworkroute "github.com/goravel/framework/route"
	frameworksession "github.com/goravel/framework/session"
	frameworktesting "github.com/goravel/framework/testing"
	frameworkvalidation "github.com/goravel/framework/validation"
	frameworkview "github.com/goravel/framework/view"
	gin "github.com/goravel/gin"
	ginfacades "github.com/goravel/gin/facades"
)

// Boot initializes all configurations
func Boot(app contractsfoundation.Application) {
	facades.Config().Add("database", map[string]any{
		// Default database connection
		"default": env("DB_CONNECTION", "postgres"),

		// Database connections
		"connections": map[string]any{
			"postgres": map[string]any{
				"driver":   "postgres",
				"host":     env("DB_HOST", "127.0.0.1"),
				"port":     envInt("DB_PORT", 5432),
				"database": env("DB_DATABASE", "vault"),
				"username": env("DB_USERNAME", "postgres"),
				"password": env("DB_PASSWORD", ""),
				"charset":  "utf8mb4",
				"prefix":   "",
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
		"name":     env("APP_NAME", "Vault"),
		"env":      env("APP_ENV", "production"),
		"debug":    envBool("APP_DEBUG", false),
		"timezone": env("APP_TIMEZONE", "UTC"),
		"locale":   env("APP_LOCALE", "en"),
		"key":      env("APP_KEY", ""),
		"url":      env("APP_URL", "http://localhost"),
		"providers": []contractsfoundation.ServiceProvider{
			&frameworklog.ServiceProvider{},
			&frameworkhttp.ServiceProvider{},
			&frameworksession.ServiceProvider{},
			&frameworkvalidation.ServiceProvider{},
			&frameworkview.ServiceProvider{},
			&gin.ServiceProvider{},
			&frameworkroute.ServiceProvider{},
			&frameworktesting.ServiceProvider{},
		},
	})

	facades.Config().Add("http", map[string]any{
		"default": "gin",
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
