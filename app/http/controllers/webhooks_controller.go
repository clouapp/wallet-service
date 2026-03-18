package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macromarkets/vault/app/container"
)

// CreateWebhook godoc
// @Summary      Create a webhook
// @Description  Registers a webhook endpoint to receive event notifications. Supported events: deposit.confirmed, withdrawal.confirmed, withdrawal.failed
// @Tags         Webhooks
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Param        body  body      CreateWebhookRequest  true  "Webhook configuration"
// @Success      201   {object}  models.WebhookConfig
// @Failure      400   {object}  ErrorResponse  "Missing required fields"
// @Failure      500   {object}  ErrorResponse
// @Router       /v1/webhooks [post]
func CreateWebhook(ctx http.Context) http.Response {
	var req struct {
		URL    string   `json:"url" form:"url"`
		Secret string   `json:"secret" form:"secret"`
		Events []string `json:"events" form:"events"`
	}
	if err := ctx.Request().Bind(&req); err != nil {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": err.Error(),
		})
	}
	if req.URL == "" || req.Secret == "" || len(req.Events) == 0 {
		return ctx.Response().Json(http.StatusBadRequest, http.Json{
			"error": "url, secret, and events are required",
		})
	}

	cfg, err := container.Get().WebhookService.CreateConfig(ctx.Context(), req.URL, req.Secret, req.Events)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Json(http.StatusCreated, cfg)
}

// ListWebhooks godoc
// @Summary      List webhooks
// @Description  Returns all registered webhook configurations
// @Tags         Webhooks
// @Produce      json
// @Security     ApiKeyAuth
// @Security     SignatureAuth
// @Success      200  {object}  WebhookConfigListResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /v1/webhooks [get]
func ListWebhooks(ctx http.Context) http.Response {
	configs, err := container.Get().WebhookService.ListConfigs(ctx.Context())
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{
			"error": err.Error(),
		})
	}
	return ctx.Response().Success().Json(http.Json{
		"data": configs,
	})
}

// CreateWebhookRequest is the request body for registering a webhook.
type CreateWebhookRequest struct {
	URL    string   `json:"url"    example:"https://example.com/webhook"`
	Secret string   `json:"secret" example:"my-webhook-secret"`
	Events []string `json:"events" example:"deposit.confirmed,withdrawal.confirmed"`
}
