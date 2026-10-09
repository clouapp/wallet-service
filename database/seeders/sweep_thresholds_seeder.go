package seeders

import (
	"context"

	"github.com/macrowallets/waas/app/repositories/chaincatalog"
)

// SweepThresholdsSeeder populates per-chain sweep + gas-readiness thresholds.
// Must run after ChainSeeder so the rows it UPDATEs exist.
type SweepThresholdsSeeder struct {
	Catalog *chaincatalog.Catalog
}

func (s *SweepThresholdsSeeder) Signature() string {
	return "SweepThresholdsSeeder"
}

func (s *SweepThresholdsSeeder) Run() error {
	return s.Catalog.SeedSweepThresholds(context.Background())
}
