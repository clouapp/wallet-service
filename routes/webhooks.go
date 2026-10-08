package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/controllers/ingest"
	"github.com/macrowallets/waas/app/http/middleware"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
)

// RegisterInboundWebhooks registers provider ingest callbacks. ProviderSignature
// checks the provider signature before the body is parsed. There is no
// dashboard or API token auth on this route.
func RegisterInboundWebhooks() {
	ingestCtrl := ingest.NewIngestController(ingest.IngestControllerDeps{
		Ingest: container.MustMake[*ingestsvc.Service](),
		Lookup: container.MustMake[*ingestsvc.Catalog]().Lookup,
	})
	facades.Route().Prefix("/v1/webhooks/ingest").Middleware(middleware.ProviderSignature()).Group(func(router route.Router) {
		router.Post("/{provider}/{chainID}", ingestCtrl.HandleWebhookIngest)
	})
}
