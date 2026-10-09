// Package health shapes the answer of the process health check.
package health

import "github.com/macrowallets/waas/app/services/deposit"

const (
	statusOK = "ok"
	// version is the build the health check reports.
	version = "0.1.0"

	scannerHealthy      = "ok"
	scannerPending      = "pending_blocks"
	scannerUnobservable = "pending_store_unavailable"
)

// Health is the public health payload. DepositScanner reports blocks whose
// deposits failed to record and still wait for a retry.
type Health struct {
	Status         string         `json:"status" example:"ok"`
	Version        string         `json:"version" example:"0.1.0"`
	DepositScanner DepositScanner `json:"deposit_scanner"`
}

// DepositScanner reports pending deposit blocks per chain. Status is ok,
// pending_blocks, or pending_store_unavailable.
type DepositScanner struct {
	Status       string         `json:"status" example:"ok"`
	Pending      map[string]int `json:"pending"`
	PendingTotal int            `json:"pending_total" example:"0"`
	Error        string         `json:"error,omitempty"`
}

// NewHealth projects the deposit scanner's pending blocks. The provider error
// stays off the wire.
func NewHealth(pending deposit.PendingHealth) Health {
	scanner := DepositScanner{Status: scannerHealthy, Pending: pending.Counts, PendingTotal: pending.Total}
	if pending.Total > 0 {
		scanner.Status = scannerPending
	}
	if pending.Err != nil {
		scanner.Status = scannerUnobservable
		scanner.Error = "pending store unavailable"
	}
	return Health{Status: statusOK, Version: version, DepositScanner: scanner}
}
