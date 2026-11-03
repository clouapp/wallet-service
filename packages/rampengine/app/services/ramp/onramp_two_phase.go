package ramp

import (
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// OnrampNeedsDelivery is an order-first on-ramp whose provider holds crypto
// after the pay-in and needs a second hop (Ozav today).
func OnrampNeedsDelivery(tx *models.Transaction) bool {
	if tx == nil || tx.Type != models.TransactionTypeOnramp {
		return false
	}
	if tx.VirtualAccountID != nil {
		return false
	}
	return tx.Provider == models.ProviderOzav
}

// OnrampCredited reports self-custody: the on-chain signature is on the row, or
// the delivery was placed and Ozav closed the order. Ozav exposes the hash on a
// separate movement no event or read carries, so a delivered order may never
// have one.
func OnrampCredited(tx *models.Transaction) bool {
	if tx == nil {
		return false
	}
	if tx.DepositTxSignature != nil && strings.TrimSpace(*tx.DepositTxSignature) != "" {
		return true
	}
	return tx.Status == models.TxStatusCompleted &&
		tx.DeliveryOrderID != nil && strings.TrimSpace(*tx.DeliveryOrderID) != ""
}

// PayInReadyForDelivery is true when the pay-in settled but delivery has not
// started or finished on-chain. funds_received does not qualify: the BRL is in
// but the provider is still buying the crypto, so its wallet cannot cover the
// send yet and the delivery would fail with an insufficient balance.
func PayInReadyForDelivery(tx *models.Transaction) bool {
	if !OnrampNeedsDelivery(tx) || OnrampCredited(tx) {
		return false
	}
	if tx.DeliveryOrderID != nil && strings.TrimSpace(*tx.DeliveryOrderID) != "" {
		return false
	}
	switch tx.Status {
	case models.TxStatusProcessing, models.TxStatusCompleted:
		return true
	default:
		return false
	}
}

// NeedsProviderRefresh keeps Show/reconcile polling until credited or terminal.
func NeedsProviderRefresh(tx *models.Transaction) bool {
	if !OnrampNeedsDelivery(tx) || OnrampCredited(tx) {
		return false
	}
	switch tx.Status {
	case models.TxStatusFailed, models.TxStatusExpired, models.TxStatusRefunded:
		return false
	}
	return tx.GatewayOrderID != "" ||
		(tx.DeliveryOrderID != nil && strings.TrimSpace(*tx.DeliveryOrderID) != "")
}

// ShouldSetCompletedAt on a two-phase on-ramp only when credited.
func ShouldSetCompletedAt(tx *models.Transaction, newStatus string) bool {
	if newStatus != models.TxStatusCompleted {
		return false
	}
	if OnrampNeedsDelivery(tx) {
		return OnrampCredited(tx)
	}
	return true
}
