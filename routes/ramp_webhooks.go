package routes

import (
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
	ctrl "github.com/macrowallets/waas/app/http/controllers/ramps"
	"github.com/macrowallets/waas/app/services/ramps"
)

func RegisterRampWebhooks() {
	controller := ctrl.NewWebhookController(container.MustMake[*ramps.Service]())
	facades.Route().Post("/v1/webhooks/ramps/{environment}/{provider}", controller.Receive)
}
