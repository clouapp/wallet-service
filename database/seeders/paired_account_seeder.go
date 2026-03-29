package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// PairedAccountSeeder seeds prod + test Acme accounts and links them.
type PairedAccountSeeder struct{}

func (s *PairedAccountSeeder) Signature() string {
	return "PairedAccountSeeder"
}

func (s *PairedAccountSeeder) Run() error {
	return seeds.SeedPairedAccounts(context.Background())
}
