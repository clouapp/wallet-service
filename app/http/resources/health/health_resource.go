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

// DepositScanner reports pending deposit blocks per chain and, in Chains, how far
// each chain's deposit detection got, from what the scanner and the inbound ingest
// recorded (no chain is called). Status is ok, pending_blocks, or
// pending_store_unavailable; it does not depend on Chains.
type DepositScanner struct {
	Status       string                  `json:"status" example:"ok"`
	Pending      map[string]int          `json:"pending"`
	PendingTotal int                     `json:"pending_total" example:"0"`
	Chains       map[string]ScannedChain `json:"chains"`
	Error        string                  `json:"error,omitempty"`
	ChainsError  string                  `json:"chains_error,omitempty"`
}

// signatureMode is the Mode of a chain whose deposits the signature sync reads.
const signatureMode = "signatures"

// ScannedChain is one chain a scanner, the signature sync or a provider push touched
// in the last week. LagBlocks is head − checkpoint at the end of the last completed
// scan cycle and LastScannedAt when it ended (both absent for a chain only pushed to);
// LastPushAt is when an inbound provider webhook for the chain was last received
// (absent when none was). A chain whose scan last recorded that the signature sync
// reads it has Mode "signatures" and, instead of the block scan's fields, those of its
// last completed sync cycle: LastSyncedAt (when it ended), Accounts (how many it
// read), FailedAccounts (how many failed, read again on the next cycle),
// BackfillingAccounts (not read down to their floor yet, or left for the next cycle),
// SkippedMints (registered tokens the node does not hold as token mints), CoveredSlot
// (the slot every account was read up to by the last cycle that read them all) and
// LagBlocks (the head that cycle confirmed at − CoveredSlot: it grows only while
// cycles leave accounts unread). Mode is absent for a chain the block scan reads.
// Times are UTC, to the second.
type ScannedChain struct {
	Mode                string     `json:"mode,omitempty" example:"signatures"`
	LagBlocks           *uint64    `json:"lag_blocks,omitempty" example:"3"`
	LastScannedAt       *time.Time `json:"last_scanned_at,omitempty" example:"2026-10-20T09:30:00Z"`
	LastSyncedAt        *time.Time `json:"last_synced_at,omitempty" example:"2026-10-20T09:30:00Z"`
	Accounts            *int       `json:"accounts,omitempty" example:"36"`
	FailedAccounts      *int       `json:"failed_accounts,omitempty" example:"0"`
	BackfillingAccounts *int       `json:"backfilling_accounts,omitempty" example:"0"`
	SkippedMints        *int       `json:"skipped_mints,omitempty" example:"0"`
	CoveredSlot         *uint64    `json:"covered_slot,omitempty" example:"514458191"`
	LastPushAt          *time.Time `json:"last_push_at,omitempty" example:"2026-10-20T09:29:12Z"`
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

// NewHealth projects the deposit scanner's pending blocks and scan activity, and
// the outbound transfers left unsettled. Store errors stay off the wire.
func NewHealth(pending deposit.PendingHealth, stale deposit.StaleOutboundHealth, activity deposit.ScanActivityHealth) Health {
	scanner := DepositScanner{Status: scannerHealthy, Pending: pending.Counts, PendingTotal: pending.Total, Chains: scannedChains(activity.Chains)}
	if activity.Err != nil {
		scanner.ChainsError = "scan activity unavailable"
	}
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

// scannedChains projects the scan activity per chain, leaving out a chain with nothing
// to report; it is never nil.
func scannedChains(activity map[string]deposit.ChainActivity) map[string]ScannedChain {
	chains := make(map[string]ScannedChain, len(activity))
	for chainID, chain := range activity {
		var scanned ScannedChain
		switch {
		case chain.SignatureMode():
			scanned.Mode = signatureMode
			if sync := chain.Signatures; sync != nil {
				accounts, failed, backfilling, skipped := sync.Accounts, sync.FailedAccounts, sync.BackfillingAccounts, len(sync.SkippedMints)
				scanned.LastSyncedAt = wireTime(sync.At)
				scanned.Accounts, scanned.FailedAccounts = &accounts, &failed
				scanned.BackfillingAccounts, scanned.SkippedMints = &backfilling, &skipped
				if sync.CoveredHead > 0 {
					covered, lag := sync.CoveredHead, sync.LagBlocks()
					scanned.CoveredSlot, scanned.LagBlocks = &covered, &lag
				}
			}
		case chain.Scan != nil:
			lag := chain.Scan.LagBlocks()
			scanned.LagBlocks = &lag
			scanned.LastScannedAt = wireTime(chain.Scan.At)
		}
		if !chain.PushedAt.IsZero() {
			scanned.LastPushAt = wireTime(chain.PushedAt)
		}
		if scanned == (ScannedChain{}) {
			// Only a block scan mode, or a sync record of a chain the block scan reads,
			// and no completed scan cycle yet: nothing to report.
			continue
		}
		chains[chainID] = scanned
	}
	return chains
}

func wireTime(at time.Time) *time.Time {
	utc := at.UTC().Truncate(time.Second)
	return &utc
}
