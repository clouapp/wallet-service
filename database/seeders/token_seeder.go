package seeders

import (
	"context"

	"github.com/macrowallets/waas/app/repositories/chaincatalog"
)

// TokenSeeder seeds per-chain token contracts.
type TokenSeeder struct {
	Catalog *chaincatalog.Catalog
}

func (s *TokenSeeder) Signature() string {
	return "TokenSeeder"
}

func (s *TokenSeeder) Run() error {
	return s.Catalog.SeedTokens(context.Background())
}
