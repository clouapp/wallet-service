package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/controllers/ingest"
	"github.com/macrowallets/waas/app/repositories"
)

// RegisterInboundWebhooks registers provider ingest callbacks (no dashboard/API token auth).
func RegisterInboundWebhooks() {
	ingestCtrl := ingest.NewIngestController(
		container.MustMake[*repositories.WebhookSubscriptionRepository](),
		container.Get().IngestService,
	)
	facades.Route().Prefix("/v1/webhooks/ingest").Group(func(router route.Router) {
		router.Post("/{provider}/{chainID}", ingestCtrl.HandleWebhookIngest)
	})
}
