package ingest

import (
	"context"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	resources "github.com/macrowallets/waas/app/http/resources/ingest"
	"github.com/macrowallets/waas/app/http/responses"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
)

// inboundReceiver ingests one verified provider callback.
type inboundReceiver interface {
	Receive(ctx context.Context, hook ingestsvc.Webhook) error
}

// IngestController serves inbound provider webhooks.
type IngestController struct {
	inbound inboundReceiver
}

// NewIngestController wires the inbound webhook handler.
func NewIngestController(inbound inboundReceiver) *IngestController {
	if inbound == nil {
		panic("ingest controller: inbound webhook service is required")
	}
	return &IngestController{inbound: inbound}
}

// Store ingests one provider callback whose signature ProviderSignature has
// already verified, before the body is parsed. There is no dashboard or API
// token auth on this route, and it is not part of the documented API.
func (c *IngestController) Store(ctx http.Context) http.Response {
	provider, chainID, body, verified := middleware.VerifiedInboundWebhook(ctx)
	if !verified {
		return responses.Fail(ctx, http.StatusUnauthorized, responses.CodeInvalidSignature, "invalid webhook signature")
	}

	err := c.inbound.Receive(ctx.Context(), ingestsvc.Webhook{Provider: provider, ChainID: chainID, Body: body})
	if err != nil {
		return mapError(ctx, err)
	}

	return ctx.Response().Success().Json(resources.NewAccepted())
}
