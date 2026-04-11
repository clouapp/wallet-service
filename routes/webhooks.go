package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/controllers"
)

// RegisterInboundWebhooks registers provider ingest callbacks (no dashboard/API token auth).
func RegisterInboundWebhooks() {
	facades.Route().Prefix("/v1/webhooks/ingest").Group(func(router route.Router) {
		router.Post("/{provider}/{chainID}", controllers.HandleWebhookIngest)
	})
}
