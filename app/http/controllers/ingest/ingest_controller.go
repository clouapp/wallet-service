package ingest

import (
	"context"
	"log/slog"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/http/responses"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

// providerLookup returns the ingest providers for this request. The map is
// built with a KeySource, so the credential is read at verify time.
type providerLookup func() map[string]providers.WebhookProvider

// inboundIngest accepts one verified webhook as a typed event.
type inboundIngest interface {
	Ingest(ctx context.Context, event ingestsvc.InboundEvent) error
}

// IngestController serves inbound provider webhooks.
type IngestController struct {
	ingest inboundIngest
	lookup providerLookup
}

// IngestControllerDeps is everything the ingest controller needs.
// Lookup may be nil; a nil lookup resolves the adapter registered for the provider.
type IngestControllerDeps struct {
	Ingest *ingestsvc.Service
	Lookup providerLookup
}

// NewIngestController wires the inbound webhook handlers from IngestControllerDeps.
func NewIngestController(deps IngestControllerDeps) *IngestController {
	if deps.Ingest == nil {
		panic("ingest controller: ingest service is required")
	}
	return &IngestController{
		ingest: deps.Ingest,
		lookup: deps.Lookup,
	}
}

func (ctrl *IngestController) provider(name string) (providers.WebhookProvider, bool) {
	return providers.Resolve(name, ctrl.lookup)
}

func (ctrl *IngestController) HandleWebhookIngest(ctx http.Context) http.Response {
	providerName, chainID, rawBody, verified := middleware.VerifiedInboundWebhook(ctx)
	if !verified {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid webhook signature"})
	}
	provider, found := ctrl.provider(providerName)
	if !found {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "unknown provider"})
	}
	transfers, err := provider.ParsePayload(rawBody)
	if err != nil {
		slog.Warn("ingest parse rejected", "provider", providerName)
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid payload"})
	}
	if err := ctrl.ingest.Ingest(ctx.Context(), ingestsvc.InboundEvent{
		ChainID:   chainID,
		Transfers: transfers,
	}); err != nil {
		return responses.InternalError(ctx, err)
	}
	return responses.Send(ctx, http.StatusOK, http.Json{"ok": true})
}
