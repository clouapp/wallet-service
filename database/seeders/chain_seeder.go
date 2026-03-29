package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// ChainSeeder seeds chains (mainnets + testnets).
type ChainSeeder struct{}

func (s *ChainSeeder) Signature() string {
	return "ChainSeeder"
}

func (s *ChainSeeder) Run() error {
	return seeds.SeedChains(context.Background())
}
