package routes

// RegisterHTTP registers all HTTP route groups (docs, ingest, dashboard, external API).
func RegisterHTTP() {
	RegisterDocs()
	RegisterInboundWebhooks()
	RegisterAdminRoutes()
	RegisterExternalAPI()
}
