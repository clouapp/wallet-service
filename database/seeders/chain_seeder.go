package seeders

import (
	"context"

	"github.com/macrowallets/waas/app/repositories/chaincatalog"
)

// ChainSeeder seeds chains (mainnets + testnets).
type ChainSeeder struct {
	Catalog *chaincatalog.Catalog
}

func (s *ChainSeeder) Signature() string {
	return "ChainSeeder"
}

func (s *ChainSeeder) Run() error {
	return s.Catalog.SeedChains(context.Background())
}
