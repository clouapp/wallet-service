package providers

import (
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/database/migrations"
)

type MigrationsServiceProvider struct{}

func (r *MigrationsServiceProvider) Register(app foundation.Application) {}

func (r *MigrationsServiceProvider) Boot(app foundation.Application) {
	// Register all migrations with the schema
	if schema := facades.Schema(); schema != nil {
		schema.Register(migrations.All())
	}
}
