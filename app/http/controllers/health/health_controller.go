package health

import (
	"github.com/goravel/framework/contracts/http"

	resources "github.com/macrowallets/waas/app/http/resources/health"
	"github.com/macrowallets/waas/app/services/deposit"
)

// HealthController serves the process health check.
type HealthController struct {
	deposits *deposit.Service
}

// NewHealthController wires the deposit scanner used to report pending blocks.
func NewHealthController(deposits *deposit.Service) *HealthController {
	if deposits == nil {
		panic("health controller: deposit service is required")
	}
	return &HealthController{deposits: deposits}
}

// Show godoc
//
//	@Summary		Health check
//	@Description	Returns service status and version, and how many deposit blocks per chain wait for a retry
//	@Tags			System
//	@Produce		json
//	@Success		200	{object}	resources.Health
//	@Router			/health [get]
func (c *HealthController) Show(ctx http.Context) http.Response {
	return ctx.Response().Success().Json(resources.NewHealth(c.deposits.PendingHealth(ctx.Context())))
}
