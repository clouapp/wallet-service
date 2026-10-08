package facades

import (
	"github.com/goravel/framework/contracts/database/seeder"
)

// Seeder returns the database seeder runner.
func Seeder() seeder.Facade {
	return App().MakeSeeder()
}
