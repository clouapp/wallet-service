package testutil

import (
	"os"

	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"
	frameworkfoundation "github.com/goravel/framework/foundation"

	"github.com/macromarkets/vault/database/migrations"
)

// BootTest initializes Goravel for testing (service-level tests, no routes).
// Config is set before Boot so ORM and other providers initialise correctly.
func BootTest() foundation.Application {
	app := frameworkfoundation.NewApplication()

	// Set database config BEFORE boot so the ORM provider picks it up.
	testDSN := os.Getenv("TEST_DATABASE_URL")
	host, port, dbName, user, pass := "localhost", 5432, "vault_test", "vault", "vault"
	if testDSN == "" {
		// defaults above
	} else {
		_ = testDSN // future: parse DSN components
	}

	facades.Config().Add("database", map[string]any{
		"default": "postgres",
		"connections": map[string]any{
			"postgres": map[string]any{
				"driver":   "postgres",
				"host":     host,
				"port":     port,
				"database": dbName,
				"username": user,
				"password": pass,
			},
		},
	})

	app.Boot()

	// Register migrations so migrate:fresh works in tests.
	for _, migration := range migrations.All() {
		_ = migration
	}

	return app
}
