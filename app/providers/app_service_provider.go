package providers

import (
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/routes"
)

type AppServiceProvider struct {
}

func NewAppServiceProvider() *AppServiceProvider {
	return &AppServiceProvider{}
}

func (receiver *AppServiceProvider) Register(app foundation.Application) {
	// Service registration
}

func (receiver *AppServiceProvider) Boot(app foundation.Application) {
	// Register HTTP routes
	facades.Route().GlobalMiddleware()

	// API routes
	routes.Api()
}
