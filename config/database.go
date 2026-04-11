package config

import (
	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	"github.com/goravel/framework/facades"
	postgres_facades "github.com/goravel/postgres/facades"
)

func registerDatabase() {
	facades.Config().Add("database", map[string]any{
		"default": envString("DB_CONNECTION", "postgres"),
		"connections": map[string]any{
			"postgres": map[string]any{
				"driver":   "postgres",
				"host":     envString("DB_HOST", "127.0.0.1"),
				"port":     envInt("DB_PORT", 5432),
				"database": envString("DB_DATABASE", "vault"),
				"username": envString("DB_USERNAME", "postgres"),
				"password": envString("DB_PASSWORD", ""),
				"sslmode":  envString("DB_SSLMODE", "disable"),
				"charset":  "utf8",
				"prefix":   "",
				"via": func() (contractsdriver.Driver, error) {
					return postgres_facades.Postgres("postgres")
				},
			},
		},
		"redis": map[string]any{
			"default": map[string]any{
				"host":     envString("REDIS_HOST", "127.0.0.1"),
				"port":     envString("REDIS_PORT", "6379"),
				"password": envString("REDIS_PASSWORD", ""),
				"database": envInt("REDIS_DB", 0),
			},
		},
		"migrations": map[string]any{
			"table": "migrations",
		},
		"pool": map[string]any{
			"max_idle_conns": envInt("DB_MAX_IDLE_CONNS", 10),
			"max_open_conns": envInt("DB_MAX_OPEN_CONNS", 100),
			"max_lifetime":   envInt("DB_MAX_LIFETIME", 3600),
		},
	})
}
