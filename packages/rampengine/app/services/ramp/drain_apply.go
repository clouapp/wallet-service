package ramp

import (
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// DrainCanLeaveTerminal is models.CanLeaveTerminal for a payout's drain. A
// drain can come back after it was paid out: the bank returns the ACH or wire
// days after payment_processed, and Bridge reports returned and
// refund_in_flight (the money on its way back, processing again) and then
// refunded, or refund_failed (failed). Dropping those, as the generic guard
// does, left the payout completed while the money never reached the user.
// Only a completed payout moves this way, and only on a return: any other late
// report still cannot walk it back.
func DrainCanLeaveTerminal(current string, drain *ramp.LiquidationDrain) bool {
	if models.CanLeaveTerminal(current, drain.Status) {
		return true
	}
	return current == models.TxStatusCompleted && drain.Returning
}

// DrainMayMove reports whether a drain report may move tx. A terminal payout
// moves only as DrainCanLeaveTerminal allows. A payout the bank returned is
// processing again, and it leaves only to refunded or failed: a redelivered,
// re-read or late payment_processed (or payment_submitted) must not flip it
// back.
func DrainMayMove(tx *models.Transaction, drain *ramp.LiquidationDrain) bool {
	if models.IsTerminalStatus(tx.Status) {
		return DrainCanLeaveTerminal(tx.Status, drain)
	}
	if drainReturned(tx) {
		return drain.Status == models.TxStatusRefunded || drain.Status == models.TxStatusFailed ||
			drain.Returning
	}
	return true
}

// drainReturned reports a payout the bank sent back, while it is processing
// again. Returned after completion, it keeps the CompletedAt of its
// payment_processed (only a return takes a completed payout out of
// completed). Returned before our payment_processed arrived, the return's
// reason is what the row keeps of it: a drain's FailureReason comes only from
// its return details.
func drainReturned(tx *models.Transaction) bool {
	return tx.Type == models.TransactionTypeOfframp && (tx.CompletedAt != nil || tx.FailureReason != nil)
}

// ApplyLiquidationDrain writes what one drain report knows onto the row, for
// the webhook, the reconciler and the sandbox simulation alike, so the three
// cannot disagree.
//
// A row with no ExternalID is new: the drain is its whole identity, and the
// address names the user, the rail, the token and the bank account. The token
// leg is what arrived; the fiat leg (AmountFiat) is the USD the bank was sent,
// estimated (estimatedDrainFiat) until a report carries it.
func ApplyLiquidationDrain(tx *models.Transaction, address *models.UserLiquidationAddress, drain *ramp.LiquidationDrain) {
	if tx.ExternalID == "" {
		openDrainTransaction(tx, address, drain)
	}
	if drain.Status != "" {
		tx.Status = drain.Status
	}
	tx.AmountToken = firstNonEmpty(drain.SourceAmount, tx.AmountToken, zeroAmount)
	tx.AmountFiat = firstNonEmpty(drain.DestinationAmount, tx.AmountFiat, zeroAmount)
	if drain.DestinationAmount == "" && decimalEqual(tx.AmountFiat, zeroAmount) {
		tx.AmountFiat = estimatedDrainFiat(tx.AmountToken, drain.FeeAmount, tx.FeePercent)
	}
	if drain.FeeAmount != "" {
		// Bridge takes the developer fee in the token, before conversion.
		fee, currency := drain.FeeAmount, strings.ToLower(address.Currency)
		tx.FeeAmount, tx.FeeCurrency = &fee, &currency
	}
	if drain.PaymentRail != "" {
		paymentRail := drain.PaymentRail
		tx.PaymentRail = &paymentRail
	}
	if drain.FromAddress != "" {
		from := drain.FromAddress
		tx.FromAddress = &from
	}
	if drain.DepositTxHash != "" {
		hash := drain.DepositTxHash
		tx.DepositTxSignature = &hash
	}
	if drain.FailureReason != "" {
		reason := drain.FailureReason
		tx.FailureReason = &reason
	}
	if tx.Status == models.TxStatusCompleted && tx.CompletedAt == nil {
		completed := time.Now()
		if drain.OccurredAt != nil {
			completed = *drain.OccurredAt
		}
		tx.CompletedAt = &completed
	}
}

// estimatedDrainFiat is the USD a payout is expected to send before Bridge
// reports what it did: the token less the developer fee, 1:1 (the address
// drains a USD stablecoin). The fee is the one Bridge reported, else the
// address's percent. The receipt's destination amount replaces it, so the
// list never shows US$ 0,00 for crypto that already arrived. Rounded down to
// cents, as the column holds.
func estimatedDrainFiat(token, feeAmount string, feePercent *string) string {
	amount, ok := new(big.Rat).SetString(token)
	if !ok || amount.Sign() <= 0 {
		return zeroAmount
	}
	if fee, ok := new(big.Rat).SetString(feeAmount); ok {
		amount.Sub(amount, fee)
	} else if feePercent != nil {
		if percent, ok := new(big.Rat).SetString(*feePercent); ok {
			keep := new(big.Rat).Sub(big.NewRat(1, 1), percent.Quo(percent, big.NewRat(100, 1)))
			amount.Mul(amount, keep)
		}
	}
	if amount.Sign() <= 0 {
		return zeroAmount
	}
	cents := new(big.Int).Quo(new(big.Int).Mul(amount.Num(), big.NewInt(100)), amount.Denom())
	return new(big.Rat).SetFrac(cents, big.NewInt(100)).FloatString(2)
}

// openDrainTransaction is the identity of a payout a drain opens. ExpiresAt
// stays nil: the crypto arrived before the row existed, so there is no payment
// window for Reconciler.expire to close. The payload names the bank account:
// persisted, never logged.
func openDrainTransaction(tx *models.Transaction, address *models.UserLiquidationAddress, drain *ramp.LiquidationDrain) {
	rail, chain, drainID := address.Rail, address.Chain, drain.DrainID
	addressID, bankAccountID := address.ID, address.UserBankAccountID
	// The payout is the address's customer's; the member who provisioned the
	// address is its actor, as a deposit's is the virtual account's.
	tx.CustomerID = address.CustomerID
	tx.UserID = address.UserID
	tx.Provider = address.Provider
	tx.Type = models.TransactionTypeOfframp
	tx.Rail = &rail
	tx.FiatCurrency = models.FiatCurrencyUSD
	tx.ExternalID = uuid.NewString()
	tx.GatewayOrderID = drainID
	tx.DepositID = &drainID
	tx.TargetToken = strings.ToUpper(address.Currency)
	tx.Chain = &chain
	tx.DestinationWallet = address.Address
	tx.LiquidationAddressID = &addressID
	tx.BankAccountID = &bankAccountID
	tx.FeePercent = address.FeePercent
	tx.RawPayload = rawPayload(string(drain.Raw))
}

// DrainChangedFromProvider reports whether applying drain would change what
// the client reads, so a refresh writes only when something moved.
func DrainChangedFromProvider(tx *models.Transaction, address *models.UserLiquidationAddress, drain *ramp.LiquidationDrain) bool {
	scratch := *tx
	ApplyLiquidationDrain(&scratch, address, drain)
	return orderSnapshotChanged(tx, &scratch)
}
