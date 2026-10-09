package routes

import (
	"crypto/sha256"
	"encoding/base64"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers/health"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/docs"
)

// swaggerBootScript starts the UI. It is inline, so the page's policy allows it
// by hash instead of 'unsafe-inline'.
const swaggerBootScript = `
  SwaggerUIBundle({
    url: "/swagger/doc.json",
    dom_id: '#swagger-ui',
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
    layout: "BaseLayout",
    deepLinking: true
  })
`

const swaggerHTML = `<!DOCTYPE html>
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
<script>` + swaggerBootScript + `</script>
</body>
</html>`

// swaggerCSP replaces the API's default-src 'none' on the UI page only: it
// needs swagger-ui from unpkg and the spec from this origin.
var swaggerCSP = func() string {
	sum := sha256.Sum256([]byte(swaggerBootScript))
	return "default-src 'none'; " +
		"script-src https://unpkg.com 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; " +
		"style-src https://unpkg.com; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'"
}()

// RegisterDocs exposes health, the Swagger UI and the generated spec.
func RegisterDocs() {
	facades.Route().Get("/health", health.NewHealthController(container.MustMake[*deposit.Service]()).Show)

	facades.Route().Get("/swagger/index.html", func(ctx http.Context) http.Response {
		return ctx.Response().
			Header("Content-Security-Policy", swaggerCSP).
			Header("Content-Type", "text/html; charset=utf-8").
			String(http.StatusOK, swaggerHTML)
	})

	facades.Route().Get("/swagger/doc.json", func(ctx http.Context) http.Response {
		return ctx.Response().
			Header("Content-Type", "application/json").
			String(http.StatusOK, docs.SwaggerInfo.ReadDoc())
	})
}
