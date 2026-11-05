package ramps

import (
	"errors"
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/ramps"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/webhooks"
	lifecycle "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
	"io"
)

type WebhookController struct{ service *ramps.Service }

func NewWebhookController(s *ramps.Service) *WebhookController { return &WebhookController{s} }
func (c *WebhookController) Receive(ctx http.Context) http.Response {
	mode := ctx.Request().Route("environment")
	provider := ctx.Request().Route("provider")
	if (mode != "sandbox" && mode != "production") || (provider != "bridge" && provider != "ozav" && provider != "blindpay") {
		return responses.Fail(ctx, 401, "invalid_signature", "delivery rejected")
	}
	body, err := io.ReadAll(io.LimitReader(ctx.Request().Origin().Body, (8<<20)+1))
	if err != nil || len(body) > 8<<20 {
		return responses.Fail(ctx, 400, "invalid_request", "invalid webhook body")
	}
	headers := map[string]string{}
	for k := range ctx.Request().Origin().Header {
		headers[k] = ctx.Request().Origin().Header.Get(k)
	}
	runtime, err := c.service.RuntimeMode(uuid.Nil, mode, nil)
	if err != nil {
		return responses.InternalError(ctx, err)
	}
	err = runtime.Receiver.Receive(ctx.Context(), dto.ReceiveWebhookDTO{Provider: provider, Headers: headers, Body: body})
	if err != nil {
		if errors.Is(err, lifecycle.ErrWebhookRejected) {
			return responses.Fail(ctx, 401, "invalid_signature", "delivery rejected")
		}
		return responses.InternalError(ctx, err)
	}
	return ctx.Response().Status(202).Json(http.Json{"received": true})
}
