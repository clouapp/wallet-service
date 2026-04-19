package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// SweepThresholdsSeeder populates per-chain sweep + gas-readiness thresholds.
// Must run after ChainSeeder so the rows it UPDATEs exist.
type SweepThresholdsSeeder struct{}

func (s *SweepThresholdsSeeder) Signature() string {
	return "SweepThresholdsSeeder"
}

func (s *SweepThresholdsSeeder) Run() error {
	return seeds.SeedSweepThresholds(context.Background())
}
