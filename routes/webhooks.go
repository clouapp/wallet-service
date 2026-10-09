package routes

import (
	"github.com/goravel/framework/contracts/route"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/controllers/ingest"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
)

// RegisterInboundWebhooks registers provider ingest callbacks. The global chain
// runs ProviderSignature (middleware.GlobalChain), which checks the provider
// signature before the body is parsed; a second copy here would only be skipped.
// There is no dashboard or API token auth on this route.
func RegisterInboundWebhooks() {
	ingestCtrl := ingest.NewIngestController(ingestsvc.NewInbound(
		container.MustMake[*ingestsvc.Service](),
		container.MustMake[*ingestsvc.Catalog]().Lookup,
	))
	facades.Route().Prefix("/v1/webhooks/ingest").Group(func(router route.Router) {
		router.Post("/{provider}/{chainID}", ingestCtrl.Store)
	})
}
