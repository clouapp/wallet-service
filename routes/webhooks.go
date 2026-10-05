package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/controllers/ingest"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
)

// RegisterInboundWebhooks registers provider ingest callbacks (no dashboard/API token auth).
func RegisterInboundWebhooks() {
	ingestCtrl := ingest.NewIngestController(ingest.IngestControllerDeps{
		Subscriptions: container.MustMake[*ingestsvc.Subscriptions](),
		Ingest:        container.MustMake[*ingestsvc.Service](),
		Lookup:        container.MustMake[*ingestsvc.Catalog]().Lookup,
	})
	facades.Route().Prefix("/v1/webhooks/ingest").Group(func(router route.Router) {
		router.Post("/{provider}/{chainID}", ingestCtrl.HandleWebhookIngest)
	})
}
