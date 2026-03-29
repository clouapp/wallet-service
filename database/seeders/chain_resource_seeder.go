package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// ChainResourceSeeder seeds explorer / faucet links per chain.
type ChainResourceSeeder struct{}

func (s *ChainResourceSeeder) Signature() string {
	return "ChainResourceSeeder"
}

func (s *ChainResourceSeeder) Run() error {
	return seeds.SeedChainResources(context.Background())
}
