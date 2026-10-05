package ingest

import (
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

var ingestProviders = map[string]providers.WebhookProvider{
	"helius":    providers.NewHeliusProvider(""),
	"quicknode": providers.NewQuickNodeProvider(""),
}

// providerLookup returns the ingest providers for this request. The map is
// built with a KeySource, so the credential is read at verify time.
type providerLookup func() map[string]providers.WebhookProvider

// IngestController serves inbound provider webhooks.
type IngestController struct {
	subscriptions *ingestsvc.Subscriptions
	ingest        *ingestsvc.Service
	lookup        providerLookup
}

// IngestControllerDeps is everything the ingest controller needs.
// Lookup may be nil; a nil lookup keeps the package provider map.
type IngestControllerDeps struct {
	Subscriptions *ingestsvc.Subscriptions
	Ingest        *ingestsvc.Service
	Lookup        providerLookup
}

// NewIngestController wires the inbound webhook handlers from IngestControllerDeps.
func NewIngestController(deps IngestControllerDeps) *IngestController {
	if deps.Subscriptions == nil {
		panic("ingest controller: webhook subscriptions service is required")
	}
	if deps.Ingest == nil {
		panic("ingest controller: ingest service is required")
	}
	return &IngestController{
		subscriptions: deps.Subscriptions,
		ingest:        deps.Ingest,
		lookup:        deps.Lookup,
	}
}

func (ctrl *IngestController) provider(name string) (providers.WebhookProvider, bool) {
	if ctrl != nil && ctrl.lookup != nil {
		if found, ok := ctrl.lookup()[name]; ok && found != nil {
			return found, true
		}
	}
	// The Alchemy client is registered by its adapter during init, so this
	// fallback is resolved on the request rather than in the package map.
	if name == "alchemy" {
		found := providers.NewAlchemyProvider("")
		return found, found != nil
	}
	found, ok := ingestProviders[name]
	return found, ok
}

func (ctrl *IngestController) HandleWebhookIngest(ctx http.Context) http.Response {
	req := ctx.Request().Origin()
	rawBody, err := io.ReadAll(req.Body)
	if err != nil {
		slog.Error("ingest read body", "error", err)
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid body"})
	}

	providerName := strings.ToLower(strings.TrimSpace(ctx.Request().Input("provider")))
	chainID := strings.TrimSpace(ctx.Request().Input("chainID"))
	if providerName == "" || chainID == "" {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "provider and chainID are required"})
	}

	sub, err := ctrl.subscriptions.FindByProviderAndChain(ctx.Context(), providerName, chainID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		sub, err = nil, nil
	}
	if err != nil {
		slog.Error("ingest subscription lookup", "provider", providerName, "chain", chainID, "error", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "subscription lookup failed"})
	}
	if sub == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "webhook subscription not found"})
	}

	secret, err := facades.Crypt().DecryptString(sub.SigningSecret)
	if err != nil {
		slog.Error("ingest decrypt signing secret", "error", err)
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "configuration error"})
	}

	provider, found := ctrl.provider(providerName)
	if !found {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "unknown provider"})
	}

	valid, verifyErr := provider.VerifyInbound(providers.Header(req.Header), rawBody, secret)
	if verifyErr != nil {
		slog.Warn("ingest verify", "provider", providerName, "error", verifyErr)
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid webhook signature"})
	}
	if !valid {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "invalid webhook signature"})
	}

	transfers, err := provider.ParsePayload(rawBody)
	if err != nil {
		slog.Warn("ingest parse", "provider", providerName, "error", err)
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid payload"})
	}

	if err := ctrl.ingest.ProcessTransfers(ctx.Context(), chainID, transfers); err != nil {
		slog.Warn("ingest process", "chain", chainID, "error", err)
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
	}

	return responses.Send(ctx, http.StatusOK, http.Json{"ok": true})
}
