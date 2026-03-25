// cmd/seed runs the database seeder against the configured database.
// Usage: make db-seed   (or: go run cmd/seed/main.go)
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/joho/godotenv"

	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/database/seeds"
)

func main() {
	// Load env — same as the main server
	_ = godotenv.Load()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	bootstrap.Boot()

	ctx := context.Background()
	if err := seeds.Run(ctx); err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
}
