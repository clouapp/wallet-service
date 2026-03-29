package seeders

import (
	"github.com/goravel/framework/contracts/database/seeder"
)

// All returns seeders registered with the framework (see bootstrap WithSeeders).
func All() []seeder.Seeder {
	return []seeder.Seeder{
		&DatabaseSeeder{},
	}
}
