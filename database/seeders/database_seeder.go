package seeders

import (
	"log/slog"

	"github.com/goravel/framework/contracts/database/seeder"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/database/seeds"
)

// DatabaseSeeder is the root seeder invoked by `artisan db:seed` and `migrate:fresh --seed`.
type DatabaseSeeder struct{}

func (s *DatabaseSeeder) Signature() string {
	return "DatabaseSeeder"
}

func (s *DatabaseSeeder) Run() error {
	slog.Info("seeding database…")
	if err := facades.Seeder().Call([]seeder.Seeder{
		&ChainSeeder{},
		&TokenSeeder{},
		&ChainResourceSeeder{},
		&PairedAccountSeeder{},
		&UserSeeder{},
		&AccountUserSeeder{},
		&WalletSeeder{},
	}); err != nil {
		return err
	}
	slog.Info("seed complete ✓")
	seeds.PrintCredentials()
	return nil
}
