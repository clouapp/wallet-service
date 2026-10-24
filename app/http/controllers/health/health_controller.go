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

// NewHealthController wires the deposit scanner used to report pending blocks and
// the scan activity.
func NewHealthController(deposits *deposit.Service) *HealthController {
	if deposits == nil {
		panic("health controller: deposit service is required")
	}
	return &HealthController{deposits: deposits}
}

// Show godoc
//
//	@Summary		Health check
//	@Description	Returns service status and version, how many deposit blocks per chain wait for a retry, how far each chain's deposit detection got (lag_blocks and last_scanned_at of the last completed scan cycle; for a chain whose deposits the signature sync reads, mode "signatures" with last_synced_at, accounts and failed_accounts of its last completed sync cycle instead; last_push_at of the last inbound provider webhook; read from what the scanner, the sync and the ingest recorded, never from the chain), and how many outbound transfers per chain are still unsettled after WITHDRAWAL_STALE_AFTER_HOURS (settle them with artisan withdrawals:reconcile)
//	@Tags			System
//	@Produce		json
//	@Success		200	{object}	resources.Health
//	@Router			/health [get]
func (c *HealthController) Show(ctx http.Context) http.Response {
	readings := c.deposits.Health(ctx.Context())
	return ctx.Response().Success().Json(resources.NewHealth(readings.Pending, readings.Stale, readings.Activity))
}
