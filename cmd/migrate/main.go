package main

import (
	"fmt"
	"log"
	"os"

	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/database"
	"github.com/goravel/framework/facades"
	goravelconfig "github.com/goravel/framework/config"

	"github.com/macromarkets/vault/bootstrap"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Initialize database configuration
	if err := initConfig(); err != nil {
		log.Fatalf("Failed to initialize config: %v", err)
	}

	// Get migrations
	migrations := bootstrap.Migrations()

	command := os.Args[1]

	switch command {
	case "up", "migrate":
		runMigrations(migrations)
	case "down", "rollback":
		rollbackMigrations(migrations)
	case "status":
		showStatus(migrations)
	case "fresh":
		freshMigrate(migrations)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func initConfig() error {
	// Load environment variables from .env.dev if it exists
	if _, err := os.Stat(".env.dev"); err == nil {
		// TODO: Load .env.dev file
	}

	// Initialize database connection using environment variables
	dbConfig := goravelconfig.NewApplication(".")
	dbConfig.Add("database", map[string]any{
		"default": os.Getenv("DB_CONNECTION"),
		"connections": map[string]any{
			"postgres": map[string]any{
				"driver":   "postgres",
				"host":     os.Getenv("DB_HOST"),
				"port":     os.Getenv("DB_PORT"),
				"database": os.Getenv("DB_DATABASE"),
				"username": os.Getenv("DB_USERNAME"),
				"password": os.Getenv("DB_PASSWORD"),
			},
		},
	})

	// Initialize database manager
	dbManager := database.NewManager()
	return dbManager.Initialize()
}

func runMigrations(migrations []schema.Migration) {
	fmt.Println("Running migrations...")

	for _, migration := range migrations {
		fmt.Printf("Migrating: %s\n", migration.Signature())
		if err := migration.Up(); err != nil {
			log.Fatalf("Migration failed: %v", err)
		}
		fmt.Printf("✓ Migrated: %s\n", migration.Signature())
	}

	fmt.Println("✅ All migrations completed successfully")
}

func rollbackMigrations(migrations []schema.Migration) {
	fmt.Println("Rolling back migrations...")

	// Reverse order for rollback
	for i := len(migrations) - 1; i >= 0; i-- {
		migration := migrations[i]
		fmt.Printf("Rolling back: %s\n", migration.Signature())
		if err := migration.Down(); err != nil {
			log.Fatalf("Rollback failed: %v", err)
		}
		fmt.Printf("✓ Rolled back: %s\n", migration.Signature())
	}

	fmt.Println("✅ All migrations rolled back successfully")
}

func showStatus(migrations []schema.Migration) {
	fmt.Println("Migration status:")
	fmt.Println("--------------------------------------------------")

	for _, migration := range migrations {
		// Check if migration table exists and if this migration has been run
		exists := facades.Schema().HasTable(migration.Signature())
		status := "Pending"
		if exists {
			status = "Migrated"
		}
		fmt.Printf("%-40s %s\n", migration.Signature(), status)
	}
}

func freshMigrate(migrations []schema.Migration) {
	fmt.Println("Dropping all tables and re-running migrations...")

	// Drop all tables
	for i := len(migrations) - 1; i >= 0; i-- {
		migration := migrations[i]
		fmt.Printf("Dropping: %s\n", migration.Signature())
		if err := migration.Down(); err != nil {
			// Ignore errors for non-existent tables
			log.Printf("Warning: %v", err)
		}
	}

	// Run all migrations
	runMigrations(migrations)
}

func printUsage() {
	fmt.Println("Vault Migration Tool")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  go run cmd/migrate/main.go <command>")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  up, migrate    Run pending migrations")
	fmt.Println("  down, rollback Rollback last batch of migrations")
	fmt.Println("  status         Show migration status")
	fmt.Println("  fresh          Drop all tables and re-run migrations")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  make migrate              # Run migrations")
	fmt.Println("  make migrate-rollback     # Rollback migrations")
	fmt.Println("  make migrate-status       # Show status")
	fmt.Println("  make migrate-fresh        # Fresh migration")
}
