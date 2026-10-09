package webhooks

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	webhooksrequests "github.com/macrowallets/waas/app/http/requests/external/webhooks"
	resources "github.com/macrowallets/waas/app/http/resources/webhooks"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/webhook"
)

// WebhookController serves the external webhook routes.
type WebhookController struct {
	webhooks *webhook.Service
}

// NewWebhookController wires the external webhook handlers.
func NewWebhookController(webhooks *webhook.Service) *WebhookController {
	if webhooks == nil {
		panic("external webhooks controller: webhook service is required")
	}
	return &WebhookController{webhooks: webhooks}
}

// Store godoc
//
//	@Summary		Create a webhook
//	@Description	Registers an account-level webhook endpoint. Deliveries are POSTed as JSON and signed with HMAC-SHA256 of the raw body in `X-Vault-Signature` (hex, keyed by `secret`); `X-Vault-Event` carries the event type and `X-Vault-Delivery-Id` the event id. Supported events include deposit.confirmed, withdrawal.broadcast, withdrawal.confirmed and withdrawal.failed. Withdrawal events are only delivered for wallets of the token's account.
//	@Tags			Webhooks
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			body	body		webhooksrequests.StoreRequest	true	"Webhook configuration"
//	@Success		201		{object}	resources.WebhookConfig
//	@Failure		400		{object}	responses.ErrorBody	"Missing required fields"
//	@Failure		500		{object}	responses.ErrorBody
//	@Failure		429		{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/api/v1/webhooks [post]
func (c *WebhookController) Store(ctx http.Context) http.Response {
	var owner *uuid.UUID
	if accountID, ok := requestctx.AccountID(ctx); ok && accountID != uuid.Nil {
		owner = &accountID
	}

	var req webhooksrequests.StoreRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	config, err := c.webhooks.CreateConfig(ctx.Context(), req.URL, req.Secret, req.Events, owner)
	if err != nil {
		return mapError(ctx, err, actionStore)
	}

	return ctx.Response().Status(http.StatusCreated).Json(resources.WebhookConfigPtr(config))
}

// Index godoc
//
//	@Summary		List webhooks
//	@Description	Returns the account's webhook configurations plus legacy unowned ones it can claim with PATCH.
//	@Tags			Webhooks
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Success		200	{object}	resources.ConfigList
//	@Failure		401	{object}	responses.ErrorBody
//	@Failure		500	{object}	responses.ErrorBody
//	@Failure		429	{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/api/v1/webhooks [get]
func (c *WebhookController) Index(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
	}

	configs, err := c.webhooks.ListAccountConfigs(ctx.Context(), accountID)
	if err != nil {
		return mapError(ctx, err, actionIndex)
	}

	return ctx.Response().Success().Json(resources.NewConfigList(configs))
}

// Update godoc
//
//	@Summary		Update a webhook subscription
//	@Description	Replaces the subscribed events and/or toggles is_active on one of the account's webhooks. A legacy webhook created before account ownership was recorded is claimed by the calling account when `secret` matches its signing secret. The secret itself is never changed or returned.
//	@Tags			Webhooks
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		SignatureAuth
//	@Param			webhookId	path		string							true	"Webhook UUID"
//	@Param			body		body		webhooksrequests.UpdateRequest	true	"Fields to change"
//	@Success		200			{object}	resources.WebhookConfig
//	@Failure		400			{object}	responses.ErrorBody	"Invalid id, empty update or unknown event"
//	@Failure		401			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody	"Secret does not match a legacy webhook"
//	@Failure		404			{object}	responses.ErrorBody	"webhook not found"
//	@Failure		429			{object}	responses.ErrorBody	"Rate limit exceeded (too_many_requests, Retry-After header)"
//	@Router			/api/v1/webhooks/{webhookId} [patch]
func (c *WebhookController) Update(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Fail(ctx, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
	}

	webhookID, err := requests.RouteUUID(ctx, "webhookId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid webhook id")
	}

	var req webhooksrequests.UpdateRequest
	if err := requests.Bind(ctx, &req); err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid request body")
	}

	config, err := c.webhooks.UpdateAccountConfig(ctx.Context(), accountID, webhookID, webhook.ConfigUpdate{
		Events:   req.Events,
		IsActive: req.IsActive,
		Secret:   req.Secret,
	})
	if err != nil {
		return mapError(ctx, err, actionUpdate)
	}

	return ctx.Response().Success().Json(resources.WebhookConfigPtr(config))
}
