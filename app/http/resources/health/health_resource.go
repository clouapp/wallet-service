// Package health shapes the answer of the process health check.
package health

import (
	"time"

	"github.com/macrowallets/waas/app/services/deposit"
)

const (
	statusOK = "ok"
	// version is the build the health check reports.
	version = "0.1.0"

	scannerHealthy      = "ok"
	scannerPending      = "pending_blocks"
	scannerUnobservable = "pending_store_unavailable"

	trackerSettled      = "ok"
	trackerStale        = "stale_transactions"
	trackerReorged      = "reorg_unresolved"
	trackerUnobservable = "transactions_store_unavailable"
)

// Health is the public health payload. DepositScanner reports blocks whose
// deposits failed to record and still wait for a retry; ConfirmationTracker the
// outbound transfers nothing settled in time.
type Health struct {
	Status              string              `json:"status" example:"ok"`
	Version             string              `json:"version" example:"0.1.0"`
	DepositScanner      DepositScanner      `json:"deposit_scanner"`
	ConfirmationTracker ConfirmationTracker `json:"confirmation_tracker"`
}

// DepositScanner reports pending deposit blocks per chain. Status is ok,
// pending_blocks, or pending_store_unavailable.
type DepositScanner struct {
	Status       string         `json:"status" example:"ok"`
	Pending      map[string]int `json:"pending"`
	PendingTotal int            `json:"pending_total" example:"0"`
	Error        string         `json:"error,omitempty"`
}

// ConfirmationTracker reports, per chain, the outbound transfers (withdrawals,
// sweeps, gas seeds) still pending or confirming stale_after_hours after they were
// recorded (withdrawals:reconcile settles them), and the deposits whose block was
// reorganized away that nothing resolved yet (reorg_unresolved: no endpoint shows
// their transaction and nothing proves it can never land; deposits:reorgs lists and
// settles them). Status is ok, stale_transactions, reorg_unresolved (no stale
// transfer, some unresolved deposit), or transactions_store_unavailable.
type ConfirmationTracker struct {
	Status               string         `json:"status" example:"ok"`
	Stale                map[string]int `json:"stale"`
	StaleTotal           int            `json:"stale_total" example:"0"`
	StaleAfterHours      int            `json:"stale_after_hours" example:"6"`
	ReorgUnresolved      map[string]int `json:"reorg_unresolved"`
	ReorgUnresolvedTotal int            `json:"reorg_unresolved_total" example:"0"`
	Error                string         `json:"error,omitempty"`
}

// NewHealth projects the deposit scanner's pending blocks and the outbound
// transfers left unsettled. Store errors stay off the wire.
func NewHealth(pending deposit.PendingHealth, stale deposit.StaleOutboundHealth) Health {
	scanner := DepositScanner{Status: scannerHealthy, Pending: pending.Counts, PendingTotal: pending.Total}
	if pending.Total > 0 {
		scanner.Status = scannerPending
	}
	if pending.Err != nil {
		scanner.Status = scannerUnobservable
		scanner.Error = "pending store unavailable"
	}
	tracker := ConfirmationTracker{
		Status: trackerSettled, Stale: stale.Counts, StaleTotal: stale.Total, StaleAfterHours: int(stale.After / time.Hour),
		ReorgUnresolved: stale.Reorged, ReorgUnresolvedTotal: stale.ReorgedTotal,
	}
	switch {
	case stale.Total > 0:
		tracker.Status = trackerStale
	case stale.ReorgedTotal > 0:
		tracker.Status = trackerReorged
	}
	if stale.Err != nil {
		tracker.Status = trackerUnobservable
		tracker.Error = "transactions store unavailable"
	}
	return Health{Status: statusOK, Version: version, DepositScanner: scanner, ConfirmationTracker: tracker}
}
