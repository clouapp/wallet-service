package ramps

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	rampsvc "github.com/macrowallets/waas/packages/rampengine/app/services/ramp"
)

// The on-ramp phases the app reads. They are a contract with installed
// builds: add to them, never rename one.
const (
	OnrampPhaseAwaitingPix = "awaiting_pix"
	OnrampPhasePixSettled  = "pix_settled"
	OnrampPhaseDelivering  = "delivering"
	OnrampPhaseCredited    = "credited"
	OnrampPhaseFailed      = "failed"
	// A dollar deposit's names for the first two: the same phases, without
	// Pix in them. A build that does not know them falls back to the status,
	// which says the same.
	OnrampPhaseAwaitingFunds = "awaiting_funds"
	OnrampPhaseFundsSettled  = "funds_settled"
)

// DeriveOnrampPhase is additive client vocabulary for two-phase on-ramps.
//
// A credit wins over the attempt count: a delivery that lands on its last
// attempt is credited, not failed.
func DeriveOnrampPhase(tx *models.Transaction, maxDeliveryAttempts int) string {
	if tx == nil || tx.Type != models.TransactionTypeOnramp {
		return ""
	}
	if rampsvc.OnrampCredited(tx) {
		return OnrampPhaseCredited
	}
	if maxDeliveryAttempts <= 0 {
		maxDeliveryAttempts = ramp.DefaultOzavDeliveryMaxAttempts
	}
	if tx.Provider == models.ProviderOzav && tx.DeliveryAttempts >= maxDeliveryAttempts {
		return OnrampPhaseFailed
	}
	switch tx.Status {
	case models.TxStatusFailed, models.TxStatusExpired, models.TxStatusRefunded:
		return OnrampPhaseFailed
	case models.TxStatusAwaitingFunds, models.TxStatusPending:
		if !paidInPix(tx) {
			return OnrampPhaseAwaitingFunds
		}
		return OnrampPhaseAwaitingPix
	case models.TxStatusFundsReceived:
		if !paidInPix(tx) {
			return OnrampPhaseFundsSettled
		}
		return OnrampPhasePixSettled
	case models.TxStatusProcessing, models.TxStatusCompleted:
		return OnrampPhaseDelivering
	default:
		return ""
	}
}

// paidInPix reports an on-ramp paid in reais. A row written before
// fiat_currency existed is one.
func paidInPix(tx *models.Transaction) bool {
	return tx.FiatCurrency == "" || tx.FiatCurrency == models.FiatCurrencyBRL
}
