package providers

import (
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/routes"
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
	facades.Route().GlobalMiddleware(middleware.Cors())

	// API routes
	routes.Api()
}
