package seeders

import (
	"log/slog"

	"github.com/goravel/framework/contracts/database/seeder"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/repositories/chaincatalog"
)

// DatabaseSeeder is the root seeder invoked by `artisan db:seed` and `migrate:fresh --seed`.
type DatabaseSeeder struct{}

func (s *DatabaseSeeder) Signature() string {
	return "DatabaseSeeder"
}

func (s *DatabaseSeeder) Run() error {
	slog.Info("seeding database…")
	catalog := chaincatalog.New(facades.Config(), facades.Crypt())
	if err := facades.Seeder().Call([]seeder.Seeder{
		&ChainSeeder{Catalog: catalog},
		&SweepThresholdsSeeder{Catalog: catalog},
		&TokenSeeder{Catalog: catalog},
		&ChainResourceSeeder{Catalog: catalog},
		&CurrencySeeder{},
		&PairedAccountSeeder{},
		&UserSeeder{},
		&AccountUserSeeder{},
		&WalletSeeder{Catalog: catalog, Config: facades.Config()},
	}); err != nil {
		return err
	}
	slog.Info("seed complete ✓")
	PrintCredentials()
	return nil
}
