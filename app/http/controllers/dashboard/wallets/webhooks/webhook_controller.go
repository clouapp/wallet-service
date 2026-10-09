package webhooks

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	"github.com/macrowallets/waas/app/http/requests"
	webhooksrequests "github.com/macrowallets/waas/app/http/requests/dashboard/wallets/webhooks"
	webhookresources "github.com/macrowallets/waas/app/http/resources/webhooks"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
)

// WebhookController serves the dashboard wallet webhook routes.
type WebhookController struct {
	configs  *walletrecords.Webhooks
	delivery *webhook.Service
}

// NewWebhookController wires the controller with the wallet webhook configs and
// the delivery service that sends the test body.
func NewWebhookController(configs *walletrecords.Webhooks, delivery *webhook.Service) *WebhookController {
	if configs == nil {
		panic("dashboard wallet webhooks controller: webhook configs service is required")
	}
	if delivery == nil {
		panic("dashboard wallet webhooks controller: webhook delivery service is required")
	}
	return &WebhookController{configs: configs, delivery: delivery}
}

// Index godoc
//
//	@Summary		List webhooks for a wallet
//	@Description	Returns all webhook configurations scoped to a wallet
//	@Tags			Wallet Webhooks
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Success		200			{object}	controllers.WebhookConfigListResponse
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/webhooks [get]
func (c *WebhookController) Index(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	configs, err := c.configs.FindByWalletID(ctx.Context(), wallet.ID)
	if err != nil {
		return mapError(ctx, err, "fetch wallet webhooks")
	}

	return ctx.Response().Success().Json(http.Json{"data": webhookresources.WebhookConfigsFrom(configs)})
}

// Store godoc
//
//	@Summary		Create a webhook for a wallet
//	@Description	Registers a webhook scoped to a specific wallet. Requires wallet or account owner/admin.
//	@Tags			Wallet Webhooks
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			walletId	path		string						true	"Wallet UUID"
//	@Param			request		body		CreateWalletWebhookSwagger	true	"Webhook configuration"
//	@Success		201			{object}	webhookresources.WebhookConfig
//	@Failure		400			{object}	responses.ErrorBody
//	@Failure		403			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/webhooks [post]
func (c *WebhookController) Store(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	var req webhooksrequests.StoreRequest
	if response := requests.Validate(ctx, &req); response != nil {
		return response
	}

	config, err := c.configs.Register(ctx.Context(), wallet.ID, req.URL, req.Secret, req.Events)
	if err != nil {
		return mapError(ctx, err, "create webhook")
	}

	return ctx.Response().Status(http.StatusCreated).Json(webhookresources.WebhookConfigPtr(config))
}

// Destroy godoc
//
//	@Summary		Delete a wallet webhook
//	@Description	Removes a webhook configuration by ID. Requires wallet or account owner/admin.
//	@Tags			Wallet Webhooks
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path	string	true	"Wallet UUID"
//	@Param			webhookId	path	string	true	"Webhook UUID"
//	@Success		204			"No content"
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/webhooks/{webhookId} [delete]
func (c *WebhookController) Destroy(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	webhookID, err := requests.RouteUUID(ctx, "webhookId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid webhook id")
	}

	if err := c.configs.Remove(ctx.Context(), wallet.ID, webhookID); err != nil {
		return mapError(ctx, err, "delete webhook")
	}

	return ctx.Response().NoContent()
}

// Test godoc
//
//	@Summary		Send a signed test webhook
//	@Description	Posts one webhook.test body to the webhook URL, signed the same way as a normal delivery. A refused URL is an error, not a success.
//	@Tags			Wallet Webhooks
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Param			webhookId	path		string	true	"Webhook UUID"
//	@Success		200			{object}	WebhookTestResponse
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		502			{object}	responses.ErrorBody
//	@Router			/wallets/{walletId}/webhooks/{webhookId}/test [post]
func (c *WebhookController) Test(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	webhookID, err := requests.RouteUUID(ctx, "webhookId")
	if err != nil {
		return responses.Fail(ctx, http.StatusBadRequest, responses.CodeInvalidRequest, "invalid webhook id")
	}

	if err := c.configs.SendTest(ctx.Context(), c.delivery, wallet.ID, webhookID); err != nil {
		return mapError(ctx, err, "send webhook test")
	}

	return ctx.Response().Success().Json(http.Json{"delivered": true})
}
