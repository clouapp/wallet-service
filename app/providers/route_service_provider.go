package providers

import (
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/routes"
)

type RouteServiceProvider struct{}

func (receiver *RouteServiceProvider) Register(app foundation.Application) {}

func (receiver *RouteServiceProvider) Boot(app foundation.Application) {
	facades.Route().GlobalMiddleware(middleware.Cors())
	routes.RegisterHTTP()
}
