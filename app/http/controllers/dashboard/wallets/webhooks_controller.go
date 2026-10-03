package wallets

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// WebhooksController serves the dashboard wallet webhook routes.
type WebhooksController struct {
	configs     *walletrecords.Webhooks
	memberships *walletrecords.Memberships
}

func NewWebhooksController(
	configs *walletrecords.Webhooks,
	memberships *walletrecords.Memberships,
) *WebhooksController {
	if configs == nil {
		panic("dashboard wallet webhooks controller: webhook configs service is required")
	}
	if memberships == nil {
		panic("dashboard wallet webhooks controller: wallet memberships are required")
	}
	return &WebhooksController{
		configs:     configs,
		memberships: memberships,
	}
}

// ListWalletWebhooks godoc
// @Summary      List webhooks for a wallet
// @Description  Returns all webhook configurations scoped to a wallet
// @Tags         Wallet Webhooks
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path  string  true  "Wallet UUID"
// @Success      200  {object}  WebhookConfigListResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/webhooks [get]
func (ctrl *WebhooksController) ListWalletWebhooks(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	cfgs, err := ctrl.configs.FindByWalletID(ctx.Context(), wallet.ID)
	if err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to fetch wallet webhooks"})
	}
	return responses.Send(ctx, http.StatusOK, http.Json{"data": controllers.WebhookConfigViews(cfgs)})
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
// @Success      201  {object}  controllers.WebhookConfigView
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/webhooks [post]
func (ctrl *WebhooksController) CreateWalletWebhook(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	if resp := controllers.Deny(ctx, policies.WalletManageWebhooks(controllers.WalletMembership(ctx, ctrl.memberships, wallet.ID))); resp != nil {
		return resp
	}

	var req requests.CreateWalletWebhookRequest
	if resp := validateRequest(ctx, &req); resp != nil {
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
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to create webhook"})
	}
	return responses.Send(ctx, http.StatusCreated, controllers.WebhookConfigViewPtr(cfg))
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
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /wallets/{walletId}/webhooks/{webhookId} [delete]
func (ctrl *WebhooksController) DeleteWalletWebhook(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)
	if resp := controllers.Deny(ctx, policies.WalletManageWebhooks(controllers.WalletMembership(ctx, ctrl.memberships, wallet.ID))); resp != nil {
		return resp
	}

	webhookID, err := requests.RouteUUID(ctx, "webhookId")
	if err != nil {
		return responses.Send(ctx, http.StatusBadRequest, http.Json{"error": "invalid webhook id"})
	}

	cfg, err := ctrl.configs.FindByIDAndWallet(ctx.Context(), webhookID, wallet.ID)
	if err != nil || cfg == nil {
		return responses.Send(ctx, http.StatusNotFound, http.Json{"error": "webhook not found"})
	}

	if err := ctrl.configs.Delete(ctx.Context(), cfg); err != nil {
		return responses.Send(ctx, http.StatusInternalServerError, http.Json{"error": "failed to delete webhook"})
	}
	return ctx.Response().NoContent()
}

// ---- Request/Response types ----

type CreateWalletWebhookSwagger struct {
	URL    string `json:"url" example:"https://example.com/hook"`
	Secret string `json:"secret,omitempty" example:"wh_secret_123"`
	Events string `json:"events,omitempty" example:"deposit.confirmed,withdrawal.confirmed"`
}
