package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// TokenSeeder seeds per-chain token contracts.
type TokenSeeder struct{}

func (s *TokenSeeder) Signature() string {
	return "TokenSeeder"
}

func (s *TokenSeeder) Run() error {
	return seeds.SeedTokens(context.Background())
}
