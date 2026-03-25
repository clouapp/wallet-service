package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
)

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
func ListWalletWebhooks(ctx http.Context) http.Response {
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	cfgs, err := container.Get().WebhookConfigRepo.FindByWalletID(wallet.ID)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch wallet webhooks"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": cfgs})
}

// CreateWalletWebhook godoc
// @Summary      Create a webhook for a wallet
// @Description  Registers a webhook scoped to a specific wallet. Requires wallet or account owner/admin.
// @Tags         Wallet Webhooks
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        walletId  path      string                    true  "Wallet UUID"
// @Param        request   body      CreateWalletWebhookRequest  true  "Webhook configuration"
// @Success      201  {object}  models.WebhookConfig
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Router       /wallets/{walletId}/webhooks [post]
func CreateWalletWebhook(ctx http.Context) http.Response {
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}
	if !isWalletAdmin(accRole, walletRole) {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only wallet/account owners and admins may create webhooks"})
	}

	var req CreateWalletWebhookRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid request body"})
	}
	if req.URL == "" {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "url is required"})
	}

	cfg := &models.WebhookConfig{
		ID:       uuid.New(),
		URL:      req.URL,
		Secret:   req.Secret,
		Events:   req.Events,
		WalletID: &wallet.ID,
		Type:     "wallet",
	}
	if err := container.Get().WebhookConfigRepo.Create(cfg); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to create webhook"})
	}
	return ctx.Response().Json(http.StatusCreated, cfg)
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
func DeleteWalletWebhook(ctx http.Context) http.Response {
	wallet, accRole, walletRole, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}
	if !isWalletAdmin(accRole, walletRole) {
		return ctx.Response().Json(http.StatusForbidden, http.Json{"error": "only wallet/account owners and admins may delete webhooks"})
	}

	webhookIDStr := ctx.Request().Route("webhookId")
	webhookID, err := uuid.Parse(webhookIDStr)
	if err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": "invalid webhook id"})
	}

	cfg, err := container.Get().WebhookConfigRepo.FindByIDAndWallet(webhookID, wallet.ID)
	if err != nil || cfg == nil {
		return ctx.Response().Json(http.StatusNotFound, http.Json{"error": "webhook not found"})
	}

	if err := container.Get().WebhookConfigRepo.Delete(cfg); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to delete webhook"})
	}
	return ctx.Response().NoContent()
}

// ---- Request/Response types ----

type CreateWalletWebhookRequest struct {
	URL    string `json:"url" example:"https://example.com/hook"`
	Secret string `json:"secret,omitempty" example:"wh_secret_123"`
	Events string `json:"events,omitempty" example:"deposit.confirmed,withdrawal.confirmed"`
}
