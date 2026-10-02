package controllers

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
)

const (
	healthPendingLookupTimeout = 2 * time.Second

	depositScannerHealthy      = "ok"
	depositScannerPending      = "pending_blocks"
	depositScannerUnobservable = "pending_store_unavailable"
)

// Health godoc
// @Summary      Health check
// @Description  Returns service status and version, and how many deposit blocks per chain wait for a retry
// @Tags         System
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Router       /health [get]
func Health(ctx http.Context) http.Response {
	return ctx.Response().Json(http.StatusOK, HealthResponse{
		Status:         "ok",
		Version:        "0.1.0",
		DepositScanner: depositScannerHealth(ctx.Context()),
	})
}

// depositScannerHealth never fails the health check: pending blocks are recovered by
// the reprocessor, so they are reported, not treated as the API being down.
func depositScannerHealth(parent context.Context) DepositScannerHealth {
	service := container.Get().DepositService
	if service == nil {
		return DepositScannerHealth{Status: depositScannerUnobservable, Error: "deposit service is not initialized"}
	}
	ctx, cancel := context.WithTimeout(parent, healthPendingLookupTimeout)
	defer cancel()
	counts, err := service.PendingCounts(ctx)
	health := DepositScannerHealth{Status: depositScannerHealthy, Pending: counts}
	for _, count := range counts {
		health.PendingTotal += count
	}
	if health.PendingTotal > 0 {
		health.Status = depositScannerPending
	}
	if err != nil {
		health.Status = depositScannerUnobservable
		health.Error = err.Error()
	}
	return health
}
