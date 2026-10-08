package seeders

import (
	"context"

	"github.com/macrowallets/waas/app/repositories/chaincatalog"
)

// ChainResourceSeeder seeds explorer / faucet links per chain.
type ChainResourceSeeder struct {
	Catalog *chaincatalog.Catalog
}

func (s *ChainResourceSeeder) Signature() string {
	return "ChainResourceSeeder"
}

func (s *ChainResourceSeeder) Run() error {
	return s.Catalog.SeedChainResources(context.Background())
}
