package config

import (
	"os"

	"github.com/goravel/framework/foundation"
)

// Boot registers all configuration into the Goravel app.
func Boot(app *foundation.Application) {
	app.MakeConfig().Add("app", map[string]interface{}{
		"name":  "vault",
		"env":   env("ENV", "dev"),
		"debug": env("ENV", "dev") == "dev",
	})

	app.MakeConfig().Add("database", map[string]interface{}{
		"default": "postgres",
		"connections": map[string]interface{}{
			"postgres": map[string]interface{}{
				"driver": "postgres",
				"dsn":    env("DATABASE_URL", "postgres://vault:vault@localhost:5432/vault?sslmode=disable"),
			},
		},
	})

	app.MakeConfig().Add("vault", map[string]interface{}{
		"eth_rpc":              env("ETH_RPC_URL", ""),
		"polygon_rpc":         env("POLYGON_RPC_URL", ""),
		"solana_rpc":          env("SOLANA_RPC_URL", ""),
		"btc_rpc":             env("BTC_RPC_URL", ""),
		"api_key_secret":      env("API_KEY_SECRET", "change-me"),
		"master_key_ref":      env("MASTER_KEY_REF", "local:dev"),
		"redis_url":           env("REDIS_URL", "redis://localhost:6379"),
		"webhook_queue_url":   env("WEBHOOK_QUEUE_URL", ""),
		"withdrawal_queue_url": env("WITHDRAWAL_QUEUE_URL", ""),
	})
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
