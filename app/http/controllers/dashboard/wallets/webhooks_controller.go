package wallets

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/resources/webhooks"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
)

// WebhooksController serves the dashboard wallet webhook routes.
type WebhooksController struct {
	configs     *walletrecords.Webhooks
	delivery    *webhook.Service
	memberships *walletrecords.Memberships
}

// WebhooksControllerDeps is everything the dashboard wallet webhooks controller needs.
// Every field is required.
type WebhooksControllerDeps struct {
	Configs     *walletrecords.Webhooks
	Delivery    *webhook.Service
	Memberships *walletrecords.Memberships
}

// NewWebhooksController wires the dashboard wallet webhook handlers from WebhooksControllerDeps.
func NewWebhooksController(deps WebhooksControllerDeps) *WebhooksController {
	if deps.Configs == nil {
		panic("dashboard wallet webhooks controller: webhook configs service is required")
	}
	if deps.Delivery == nil {
		panic("dashboard wallet webhooks controller: webhook delivery service is required")
	}
	if deps.Memberships == nil {
		panic("dashboard wallet webhooks controller: wallet memberships are required")
	}
	return &WebhooksController{
		configs:     deps.Configs,
		delivery:    deps.Delivery,
		memberships: deps.Memberships,
	}
}

// ListWalletWebhooks godoc
// @Summary      List webhooks for a wallet
// @Description  Returns all webhook configurations scoped to a wallet
// @Tags         Wallet Webhooks
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  controllers.WebhookConfigListResponse
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/webhooks [get]
func (ctrl *WebhooksController) ListWalletWebhooks(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	cfgs, err := ctrl.configs.FindByWalletID(ctx.Context(), wallet.ID)
	if err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to fetch wallet webhooks")
	}
	return ctx.Response().Success().Json(http.Json{"data": webhooks.WebhookConfigsFrom(cfgs)})
}

// CreateWalletWebhook godoc
// @Summary      Create a webhook for a wallet
// @Description  Registers a webhook scoped to a specific wallet. Requires wallet or account owner/admin.
// @Tags         Wallet Webhooks
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                    true  "Wallet UUID"
// @Param        request   body      CreateWalletWebhookSwagger  true  "Webhook configuration"
// @Success      201  {object}  webhooks.WebhookConfig
// @Failure      400  {object}  responses.ErrorBody
// @Failure      403  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/webhooks [post]
func (ctrl *WebhooksController) CreateWalletWebhook(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req requests.CreateWalletWebhookRequest
	if resp := requests.Validate(ctx, &req); resp != nil {
		return resp
	}

	cfg := &models.WebhookConfig{
		ID:       uuid.New(),
		URL:      req.URL,
		Secret:   req.Secret,
		Events:   req.Events,
		WalletID: &wallet.ID,
		Type:     "wallet",
	}
	if err := ctrl.configs.Create(ctx.Context(), cfg); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to create webhook")
	}
	return ctx.Response().Status(http.StatusCreated).Json(webhooks.WebhookConfigPtr(cfg))
}

// DeleteWalletWebhook godoc
// @Summary      Delete a wallet webhook
// @Description  Removes a webhook configuration by ID. Requires wallet or account owner/admin.
// @Tags         Wallet Webhooks
// @Security     BearerAuth
// @Produce      json
// @Param        walletId   path  string  true  "Wallet UUID"
// @Param        webhookId  path  string  true  "Webhook UUID"
// @Success      204  "No content"
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/webhooks/{webhookId} [delete]
func (ctrl *WebhooksController) DeleteWalletWebhook(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	webhookID, err := requests.RouteUUID(ctx, "webhookId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid webhook id")
	}

	cfg, err := ctrl.configs.FindByIDAndWallet(ctx.Context(), webhookID, wallet.ID)
	if err != nil || cfg == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "webhook not found")
	}

	if err := ctrl.configs.Delete(ctx.Context(), cfg); err != nil {
		return responses.Fail(ctx, http.StatusInternalServerError, responses.CodeInternal, "failed to delete webhook")
	}
	return ctx.Response().NoContent()
}

// TestWalletWebhook godoc
// @Summary      Send a signed test webhook
// @Description  Posts one webhook.test body to the webhook URL, signed the same way as a normal delivery. A refused URL is an error, not a success.
// @Tags         Wallet Webhooks
// @Security     BearerAuth
// @Produce      json
// @Param        walletId   path  string  true  "Wallet UUID"
// @Param        webhookId  path  string  true  "Webhook UUID"
// @Success      200  {object}  WebhookTestResponse
// @Failure      403  {object}  responses.ErrorBody
// @Failure      404  {object}  responses.ErrorBody
// @Failure      502  {object}  responses.ErrorBody
// @Router       /wallets/{walletId}/webhooks/{webhookId}/test [post]
func (ctrl *WebhooksController) TestWalletWebhook(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	webhookID, err := requests.RouteUUID(ctx, "webhookId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid webhook id")
	}

	cfg, err := ctrl.configs.FindByIDAndWallet(ctx.Context(), webhookID, wallet.ID)
	if err != nil || cfg == nil {
		return responses.Fail(ctx, http.StatusNotFound, responses.CodeNotFound, "webhook not found")
	}

	if err := ctrl.delivery.SendTest(ctx.Context(), cfg, wallet.ID); err != nil {
		return responses.Fail(ctx, http.StatusBadGateway, responses.CodeProviderUnavailable, "webhook test delivery failed")
	}
	return ctx.Response().Success().Json(http.Json{"delivered": true})
}

// ---- Request/Response types ----

type WebhookTestResponse struct {
	Delivered bool `json:"delivered" example:"true"`
}

type CreateWalletWebhookSwagger struct {
	URL    string `json:"url" example:"https://example.com/hook"`
	Secret string `json:"secret,omitempty" example:"wh_secret_123"`
	Events string `json:"events,omitempty" example:"deposit.confirmed,withdrawal.confirmed"`
}
