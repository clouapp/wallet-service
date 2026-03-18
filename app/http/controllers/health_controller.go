package controllers

import "github.com/goravel/framework/contracts/http"

// Health godoc
// @Summary      Health check
// @Description  Returns service status and version
// @Tags         System
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Router       /health [get]
func Health(ctx http.Context) http.Response {
	return ctx.Response().Json(http.StatusOK, http.Json{
		"status":  "ok",
		"version": "0.1.0",
	})
}
