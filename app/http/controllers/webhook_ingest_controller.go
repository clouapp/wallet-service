package controllers

import (
	"io"
	"log/slog"
	"strings"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

var ingestProviders = map[string]providers.WebhookProvider{
	"alchemy":   providers.NewAlchemyProvider(""),
	"helius":    providers.NewHeliusProvider(""),
	"quicknode": providers.NewQuickNodeProvider(""),
}

func HandleWebhookIngest(ctx http.Context) http.Response {
	c := container.Get()

	req := ctx.Request().Origin()
	rawBody, err := io.ReadAll(req.Body)
	if err != nil {
		slog.Error("ingest read body", "error", err)
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid body"})
	}

	providerName := strings.ToLower(strings.TrimSpace(ctx.Request().Input("provider")))
	chainID := strings.TrimSpace(ctx.Request().Input("chainID"))
	if providerName == "" || chainID == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "provider and chainID are required"})
	}

	sub, err := c.WebhookSubscriptionRepo.FindByProviderAndChain(providerName, chainID)
	if err != nil {
		slog.Error("ingest subscription lookup", "provider", providerName, "chain", chainID, "error", err)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "subscription lookup failed"})
	}
	if sub == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "webhook subscription not found"})
	}

	secret, err := facades.Crypt().DecryptString(sub.SigningSecret)
	if err != nil {
		slog.Error("ingest decrypt signing secret", "error", err)
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "configuration error"})
	}

	provider, found := ingestProviders[providerName]
	if !found {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "unknown provider"})
	}

	valid, verifyErr := provider.VerifyInbound(req.Header, rawBody, secret)
	if verifyErr != nil {
		slog.Warn("ingest verify", "provider", providerName, "error", verifyErr)
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid webhook signature"})
	}
	if !valid {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "invalid webhook signature"})
	}

	transfers, err := provider.ParsePayload(rawBody)
	if err != nil {
		slog.Warn("ingest parse", "provider", providerName, "error", err)
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid payload"})
	}

	if err := c.IngestService.ProcessTransfers(ctx.Context(), chainID, transfers); err != nil {
		slog.Warn("ingest process", "chain", chainID, "error", err)
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"ok": true})
}
