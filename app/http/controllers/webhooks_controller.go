package controllers

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/services/webhook"
)

// CreateWebhook godoc
// @Summary      Create a webhook
// @Description  Registers an account-level webhook endpoint. Deliveries are POSTed as JSON and signed with HMAC-SHA256 of the raw body in `X-Vault-Signature` (hex, keyed by `secret`); `X-Vault-Event` carries the event type and `X-Vault-Delivery-Id` the event id. Supported events include deposit.confirmed, withdrawal.broadcast, withdrawal.confirmed and withdrawal.failed. Withdrawal events are only delivered for wallets of the token's account.
// @Tags         Webhooks
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        body  body      CreateWebhookRequest  true  "Webhook configuration"
// @Success      201   {object}  models.WebhookConfig
// @Failure      400   {object}  ErrorResponse  "Missing required fields"
// @Failure      500   {object}  ErrorResponse
// @Router       /api/v1/webhooks [post]
func CreateWebhook(ctx http.Context) http.Response {
	var req requests.CreateWebhookRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	var owner *uuid.UUID
	if accountID, ok := ctx.Value("account_id").(uuid.UUID); ok && accountID != uuid.Nil {
		owner = &accountID
	}

	cfg, err := container.Get().WebhookService.CreateConfig(ctx.Context(), req.URL, req.Secret, req.Events, owner)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Json(http.StatusCreated, cfg)
}

// ListWebhooks godoc
// @Summary      List webhooks
// @Description  Returns the account's webhook configurations plus legacy unowned ones it can claim with PATCH.
// @Tags         Webhooks
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Success      200  {object}  WebhookConfigListResponse
// @Failure      401  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /api/v1/webhooks [get]
func ListWebhooks(ctx http.Context) http.Response {
	accountID, ok := ctx.Value("account_id").(uuid.UUID)
	if !ok || accountID == uuid.Nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	configs, err := container.Get().WebhookService.ListAccountConfigs(ctx.Context(), accountID)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": configs,
	})
}

// UpdateWebhook godoc
// @Summary      Update a webhook subscription
// @Description  Replaces the subscribed events and/or toggles is_active on one of the account's webhooks. A legacy webhook created before account ownership was recorded is claimed by the calling account when `secret` matches its signing secret. The secret itself is never changed or returned.
// @Tags         Webhooks
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        webhookId  path      string                true  "Webhook UUID"
// @Param        body       body      UpdateWebhookRequest  true  "Fields to change"
// @Success      200        {object}  models.WebhookConfig
// @Failure      400        {object}  ErrorResponse  "Invalid id, empty update or unknown event"
// @Failure      401        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse  "Secret does not match a legacy webhook"
// @Failure      404        {object}  ErrorResponse  "webhook not found"
// @Router       /api/v1/webhooks/{webhookId} [patch]
func UpdateWebhook(ctx http.Context) http.Response {
	accountID, ok := ctx.Value("account_id").(uuid.UUID)
	if !ok || accountID == uuid.Nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	webhookID, err := uuid.Parse(ctx.Request().Route("webhookId"))
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid webhook id"})
	}

	var req requests.UpdateWebhookRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	cfg, err := container.Get().WebhookService.UpdateAccountConfig(ctx.Context(), accountID, webhookID, webhook.ConfigUpdate{
		Events:   req.Events,
		IsActive: req.IsActive,
		Secret:   req.Secret,
	})
	switch {
	case err == nil:
		return ctx.Response().Json(http.StatusOK, cfg)
	case errors.Is(err, webhook.ErrWebhookConfigNotFound):
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": err.Error()})
	case errors.Is(err, webhook.ErrWebhookOwnershipNotProven):
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": err.Error()})
	case errors.Is(err, webhook.ErrWebhookUpdateEmpty),
		errors.Is(err, webhook.ErrWebhookEventsEmpty),
		errors.Is(err, webhook.ErrWebhookUnknownEvent):
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
	default:
		return MapInternalError(ctx, err, "update_webhook")
	}
}

// CreateWebhookRequest is the request body for registering a webhook.
type CreateWebhookRequest struct {
	URL    string   `json:"url"    example:"https://example.com/webhook"`
	Secret string   `json:"secret" example:"my-webhook-secret"`
	Events []string `json:"events" example:"deposit.confirmed,withdrawal.broadcast,withdrawal.confirmed,withdrawal.failed"`
}

// UpdateWebhookRequest is the request body for changing a webhook subscription.
type UpdateWebhookRequest struct {
	Events   []string `json:"events,omitempty" example:"deposit.confirmed,withdrawal.broadcast,withdrawal.confirmed,withdrawal.failed"`
	IsActive *bool    `json:"is_active,omitempty" example:"true"`
	Secret   string   `json:"secret,omitempty" example:"my-webhook-secret"`
}
