package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// UserSeeder seeds dashboard users.
type UserSeeder struct{}

func (s *UserSeeder) Signature() string {
	return "UserSeeder"
}

func (s *UserSeeder) Run() error {
	return seeds.SeedUsers(context.Background())
}
