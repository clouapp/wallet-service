// cmd/seed runs the database seeder against the configured database.
// Usage: make db-seed   (or: go run cmd/seed/main.go)
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/goravel/framework/facades"
	"github.com/joho/godotenv"

	"github.com/macromarkets/vault/bootstrap"
	"github.com/macromarkets/vault/database/seeds"
)

func main() {
	// Load env — same as the main server
	_ = godotenv.Load()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	bootstrap.Boot()

	// Run pending migrations first so the schema is up to date
	if err := facades.Artisan().Call("migrate"); err != nil {
		slog.Error("migrate failed", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := seeds.Run(ctx); err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
}
