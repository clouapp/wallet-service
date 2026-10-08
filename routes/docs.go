package routes

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/controllers/health"
	"github.com/macrowallets/waas/app/services/deposit"
)

// RegisterDocs exposes health and the Swagger UI. The spec document is
// registered by the route service provider so this package does not import
// the generated docs package.
func RegisterDocs() {
	facades.Route().Get("/health", health.NewController(container.MustMake[*deposit.Service]()).Show)

	facades.Route().Get("/swagger/index.html", func(ctx http.Context) http.Response {
		html := `<!DOCTYPE html>
<html>
<head>
  <title>Vault API - Swagger UI</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  SwaggerUIBundle({
    url: "/swagger/doc.json",
    dom_id: '#swagger-ui',
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
    layout: "BaseLayout",
    deepLinking: true
  })
</script>
</body>
</html>`
		return ctx.Response().
			Header("Content-Type", "text/html; charset=utf-8").
			String(http.StatusOK, html)
	})
}
