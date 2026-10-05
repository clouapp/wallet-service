package webhooks

import (
	"errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	webhookresource "github.com/macrowallets/waas/app/http/resources/webhooks"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/webhook"
)

func validateRequest(ctx http.Context, req http.FormRequest) http.Response {
	return controllers.ValidateRequest(ctx, req)
}

// WebhooksController serves the external webhook routes.
type WebhooksController struct {
	webhooks *webhook.Service
}

func NewWebhooksController(
	webhooks *webhook.Service,
) *WebhooksController {
	if webhooks == nil {
		panic("external webhooks controller: webhook service is required")
	}
	return &WebhooksController{
		webhooks: webhooks,
	}
}

// CreateWebhook godoc
// @Summary      Create a webhook
// @Description  Registers an account-level webhook endpoint. Deliveries are POSTed as JSON and signed with HMAC-SHA256 of the raw body in `X-Vault-Signature` (hex, keyed by `secret`); `X-Vault-Event` carries the event type and `X-Vault-Delivery-Id` the event id. Supported events include deposit.confirmed, withdrawal.broadcast, withdrawal.confirmed and withdrawal.failed. Withdrawal events are only delivered for wallets of the token's account.
// @Tags         Webhooks
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        body  body      CreateWebhookRequest  true  "Webhook configuration"
// @Success      201   {object}  webhookresource.WebhookConfig
// @Failure      400   {object}  ErrorResponse  "Missing required fields"
// @Failure      500   {object}  ErrorResponse
// @Router       /api/v1/webhooks [post]
func (ctrl *WebhooksController) CreateWebhook(ctx http.Context) http.Response {
	var req requests.CreateWebhookRequest
	if errResp := validateRequest(ctx, &req); errResp != nil {
		return errResp
	}

	var owner *uuid.UUID
	if accountID, ok := requestctx.AccountID(ctx); ok && accountID != uuid.Nil {
		owner = &accountID
	}

	cfg, err := ctrl.webhooks.CreateConfig(ctx.Context(), req.URL, req.Secret, req.Events, owner)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return responses.Send(ctx, http.StatusCreated, webhookresource.WebhookConfigPtr(cfg))
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
func (ctrl *WebhooksController) ListWebhooks(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	configs, err := ctrl.webhooks.ListAccountConfigs(ctx.Context(), accountID)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": webhookresource.WebhookConfigsFrom(configs),
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
// @Success      200        {object}  webhookresource.WebhookConfig
// @Failure      400        {object}  ErrorResponse  "Invalid id, empty update or unknown event"
// @Failure      401        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse  "Secret does not match a legacy webhook"
// @Failure      404        {object}  ErrorResponse  "webhook not found"
// @Router       /api/v1/webhooks/{webhookId} [patch]
func (ctrl *WebhooksController) UpdateWebhook(ctx http.Context) http.Response {
	accountID, ok := requestctx.AccountID(ctx)
	if !ok || accountID == uuid.Nil {
		return responses.Send(ctx, http.StatusUnauthorized, http.Json{"error": "unauthenticated"})
	}

	webhookID, err := requests.RouteUUID(ctx, "webhookId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid webhook id"})
	}

	var req requests.UpdateWebhookRequest
	if err := requests.Bind(ctx, &req); err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}

	cfg, err := ctrl.webhooks.UpdateAccountConfig(ctx.Context(), accountID, webhookID, webhook.ConfigUpdate{
		Events:   req.Events,
		IsActive: req.IsActive,
		Secret:   req.Secret,
	})
	switch {
	case err == nil:
		return responses.Send(ctx, http.StatusOK, webhookresource.WebhookConfigPtr(cfg))
	case errors.Is(err, webhook.ErrWebhookConfigNotFound):
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": err.Error()})
	case errors.Is(err, webhook.ErrWebhookOwnershipNotProven):
		return responses.Send(ctx, http.StatusForbidden, http.Json{"error": err.Error()})
	case errors.Is(err, webhook.ErrWebhookUpdateEmpty),
		errors.Is(err, webhook.ErrWebhookEventsEmpty),
		errors.Is(err, webhook.ErrWebhookUnknownEvent):
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": err.Error()})
	default:
		return controllers.MapInternalError(ctx, err, "update_webhook")
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
