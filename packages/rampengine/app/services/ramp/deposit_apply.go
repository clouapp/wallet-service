package ramp

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// zeroAmount is what a not-null amount column holds before the event that
// settles the leg arrives.
const zeroAmount = "0"

// ApplyDeposit writes what one activity knows onto the row.
//
// Every number is read, never computed — the receipt is what settles, and a
// figure of ours differing in the last place would show the user two amounts
// for one order.
func ApplyDeposit(tx *models.Transaction, account *models.CustomerVirtualAccount, deposit *ramp.VirtualAccountDeposit) {
	if deposit.Status != "" {
		tx.Status = deposit.Status
	}
	tx.AmountFiat = firstNonEmpty(deposit.SourceAmount, tx.AmountFiat, zeroAmount)
	tx.AmountToken = firstNonEmpty(deposit.DestinationAmount, tx.AmountToken, zeroAmount)
	if deposit.FeeAmount != "" {
		tx.FeeAmount = &deposit.FeeAmount
	}
	// The fiat currency comes from the account row and never from the event.
	// `currency` moves to the token on a payment activity, so the account is
	// the only source that answers for all three activity types.
	if account.SourceCurrency != "" {
		currency := account.SourceCurrency
		tx.FeeCurrency = &currency
		tx.FiatCurrency = account.SourceCurrency
	}
	if deposit.PaymentRail != "" {
		paymentRail := deposit.PaymentRail
		tx.PaymentRail = &paymentRail
	}
	if deposit.ExchangeFeeAmount != "" {
		tx.ExchangeFeeAmount = &deposit.ExchangeFeeAmount
	}
	if deposit.TxHash != "" {
		tx.DepositTxSignature = &deposit.TxHash
	}
	if deposit.FailureReason != "" {
		tx.FailureReason = &deposit.FailureReason
	}
	// PII: the only handle an operator has on a third-party deposit. Persisted,
	// never logged.
	if deposit.SenderName != "" {
		tx.SenderName = &deposit.SenderName
	}
	if deposit.SenderReference != "" {
		tx.SenderReference = &deposit.SenderReference
	}
	if tx.Status == models.TxStatusCompleted && tx.CompletedAt == nil {
		completed := time.Now()
		if deposit.OccurredAt != nil {
			completed = *deposit.OccurredAt
		}
		tx.CompletedAt = &completed
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
