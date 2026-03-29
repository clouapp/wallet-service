package seeders

import (
	"github.com/goravel/framework/contracts/database/seeder"
)

// All returns seeders registered with the framework (see bootstrap WithSeeders).
// Only DatabaseSeeder is registered: Goravel runs every registered seeder when
// `db:seed` or `migrate:fresh --seed` is invoked without `--seeder`. Sub-seeders
// are executed via facades.Seeder().Call from DatabaseSeeder.
func All() []seeder.Seeder {
	return []seeder.Seeder{
		&DatabaseSeeder{},
	}
}
