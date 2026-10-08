package health

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/services/deposit"
)

const (
	healthPendingLookupTimeout = 2 * time.Second

	depositScannerHealthy      = "ok"
	depositScannerPending      = "pending_blocks"
	depositScannerUnobservable = "pending_store_unavailable"
)

// HealthResponse is the public health payload. DepositScanner reports blocks
// whose deposits failed to record and still wait for a retry.
type HealthResponse struct {
	Status         string               `json:"status" example:"ok"`
	Version        string               `json:"version" example:"0.1.0"`
	DepositScanner DepositScannerHealth `json:"deposit_scanner"`
}

// DepositScannerHealth reports pending deposit blocks per chain. Status is
// ok, pending_blocks, or pending_store_unavailable.
type DepositScannerHealth struct {
	Status       string         `json:"status" example:"ok"`
	Pending      map[string]int `json:"pending"`
	PendingTotal int            `json:"pending_total" example:"0"`
	Error        string         `json:"error,omitempty"`
}

// Controller serves the process health check.
type Controller struct {
	deposits *deposit.Service
}

// NewController wires the deposit scanner used to report pending blocks.
func NewController(deposits *deposit.Service) *Controller {
	return &Controller{deposits: deposits}
}

// Show godoc
// @Summary      Health check
// @Description  Returns service status and version, and how many deposit blocks per chain wait for a retry
// @Tags         System
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Router       /health [get]
func (c *Controller) Show(ctx http.Context) http.Response {
	return responses.Send(ctx, http.StatusOK, HealthResponse{
		Status:         "ok",
		Version:        "0.1.0",
		DepositScanner: c.depositScannerHealth(ctx.Context()),
	})
}

// depositScannerHealth never fails the health check: pending blocks are recovered by
// the reprocessor, so they are reported, not treated as the API being down.
func (c *Controller) depositScannerHealth(parent context.Context) DepositScannerHealth {
	if c == nil || c.deposits == nil {
		return DepositScannerHealth{Status: depositScannerUnobservable, Error: "deposit service is not initialized"}
	}
	ctx, cancel := context.WithTimeout(parent, healthPendingLookupTimeout)
	defer cancel()
	counts, err := c.deposits.PendingCounts(ctx)
	health := DepositScannerHealth{Status: depositScannerHealthy, Pending: counts}
	for _, count := range counts {
		health.PendingTotal += count
	}
	if health.PendingTotal > 0 {
		health.Status = depositScannerPending
	}
	if err != nil {
		health.Status = depositScannerUnobservable
		health.Error = "pending store unavailable"
		slog.Error("deposit scanner pending counts", "error_type", fmt.Sprintf("%T", err))
	}
	return health
}
