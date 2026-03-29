package seeders

import (
	"context"

	"github.com/macrowallets/waas/database/seeds"
)

// DatabaseSeeder is the root seeder invoked by `artisan db:seed` and `migrate:fresh --seed`.
type DatabaseSeeder struct{}

func (s *DatabaseSeeder) Signature() string {
	return "DatabaseSeeder"
}

func (s *DatabaseSeeder) Run() error {
	return seeds.Run(context.Background())
}
