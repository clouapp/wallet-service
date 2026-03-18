package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macromarkets/vault/app/container"
)

// CreateWebhook creates a new webhook configuration
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

// ListWebhooks returns all webhook configurations
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
