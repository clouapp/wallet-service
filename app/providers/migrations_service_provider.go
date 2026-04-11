package providers

import (
	"github.com/goravel/framework/contracts/foundation"
)

// MigrationsServiceProvider is kept for compatibility; migrations are registered via bootstrap.WithMigrations.
type MigrationsServiceProvider struct{}

func (r *MigrationsServiceProvider) Register(app foundation.Application) {}

func (r *MigrationsServiceProvider) Boot(app foundation.Application) {}
