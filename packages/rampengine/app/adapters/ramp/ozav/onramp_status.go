package ozav

import (
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

type transferLeg struct {
	Status string `json:"status"`
}

func legCompleted(leg *transferLeg) bool {
	return leg != nil && strings.EqualFold(leg.Status, "completed")
}

// IsPayInTransfer is fiat_payment → wallet (order-first on-ramp create).
func (t transferResponse) IsPayInTransfer() bool {
	if t.Source.PaymentInstructions != nil {
		return true
	}
	return strings.EqualFold(t.Amounts.Source.Currency, "BRL") &&
		strings.EqualFold(t.Destination.Type, destinationWallet)
}

// IsDeliveryTransfer is wallet → crypto_address (second hop).
func (t transferResponse) IsDeliveryTransfer() bool {
	return strings.EqualFold(t.Destination.Type, "crypto_address")
}

// onrampPayInStatus maps a pay-in transfer to XIP canonical status (two-phase).
func onrampPayInStatus(t transferResponse) string {
	if legCompleted(t.SourceLeg) {
		// processing is what releases the delivery to the user's wallet, so it
		// waits for Ozav to report the crypto as credited (destination leg
		// completed). Until then the BRL is in but the wallet cannot cover the
		// send, which would fail with an insufficient balance.
		if strings.EqualFold(t.Status, "completed") && (t.DestLeg == nil || legCompleted(t.DestLeg)) {
			return models.TxStatusProcessing
		}
		return models.TxStatusFundsReceived
	}
	switch strings.ToLower(strings.TrimSpace(t.Status)) {
	case "awaiting_payment":
		return models.TxStatusAwaitingFunds
	case "payment_confirmed":
		return models.TxStatusFundsReceived
	case "failed", "cancelled":
		return models.TxStatusFailed
	case "expired":
		return models.TxStatusExpired
	default:
		return models.TxStatusProcessing
	}
}

// onrampDeliveryStatus maps a delivery transfer onto the order row. A send
// completes the order when Ozav reports it completed with its hash, or with
// both legs settled: the hash lives on a separate ozav_wtr_ movement that no
// event or read of the transfer carries, so waiting for it would leave every
// delivered order processing. A failure stays "processing" here and is carried
// by RampOrder.DeliveryFailed instead, which rampsvc.ApplyDeliveryOutcome turns
// into a retry.
func onrampDeliveryStatus(t transferResponse, txHash string) string {
	if !strings.EqualFold(t.Status, "completed") {
		return models.TxStatusProcessing
	}
	if txHash != "" || (legCompleted(t.SourceLeg) && legCompleted(t.DestLeg)) {
		return models.TxStatusCompleted
	}
	return models.TxStatusProcessing
}

func orderStatusFromTransfer(t transferResponse, txHash string) string {
	if t.IsDeliveryTransfer() {
		return onrampDeliveryStatus(t, txHash)
	}
	if t.IsPayInTransfer() {
		return onrampPayInStatus(t)
	}
	return transferStatusFromOzav(t.Status)
}
