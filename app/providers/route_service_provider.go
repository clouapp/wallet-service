package providers

import (
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/docs"
	"github.com/macrowallets/waas/routes"
)

type RouteServiceProvider struct{}

func (receiver *RouteServiceProvider) Register(app foundation.Application) {}

func (receiver *RouteServiceProvider) Boot(app foundation.Application) {
	facades.Route().GlobalMiddleware(middleware.Cors())
	registerSwaggerDocument()
	routes.RegisterHTTP()
}

// registerSwaggerDocument serves GET /swagger/doc.json from the generated spec.
// The composition root may import docs; the routes package may not.
func registerSwaggerDocument() {
	facades.Route().Get("/swagger/doc.json", func(ctx http.Context) http.Response {
		return ctx.Response().
			Header("Content-Type", "application/json").
			String(http.StatusOK, docs.SwaggerInfo.ReadDoc())
	})
}
