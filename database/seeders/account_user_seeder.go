package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// AccountUserSeeder seeds account membership rows.
type AccountUserSeeder struct{}

func (s *AccountUserSeeder) Signature() string {
	return "AccountUserSeeder"
}

func (s *AccountUserSeeder) Run() error {
	return seeds.SeedAccountUsers(context.Background())
}
