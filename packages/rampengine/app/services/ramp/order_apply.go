package ramp

import (
	"encoding/json"
	"math/big"
	"slices"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ApplyOrder merges the provider's view of an order onto the local row.
func ApplyOrder(tx *models.Transaction, order *ramp.RampOrder) {
	if order.ProviderOrderID != "" {
		tx.GatewayOrderID = order.ProviderOrderID
	}
	if order.Status != "" {
		tx.Status = order.Status
	}
	if order.FeeAmount != "" {
		tx.FeeAmount = &order.FeeAmount
	}
	if order.FeeCurrency != "" {
		tx.FeeCurrency = &order.FeeCurrency
	}
	if order.ExpiresAt != nil {
		tx.ExpiresAt = order.ExpiresAt
	}
	if len(order.DepositInstructions) > 0 {
		if encoded, err := json.Marshal(order.DepositInstructions); err == nil {
			instructions := string(encoded)
			tx.DepositInstructions = &instructions
		}
	}
	if len(order.Raw) > 0 {
		tx.RawPayload = string(order.Raw)
	}
	if order.TxHash != "" {
		tx.DepositTxSignature = &order.TxHash
	}
	if rate := strings.TrimSpace(order.Amounts["exchange_rate"]); rate != "" {
		tx.ExchangeRate = &rate
	}
	// The provider's own conversion cut, which XIP's fee_percent says nothing
	// about: without it the app showed 0% beside a payout under the market.
	if fee := strings.TrimSpace(order.Amounts["exchange_fee"]); fee != "" {
		tx.ExchangeFeeAmount = &fee
	}
	// The provider's settled figure wins over the quote's estimate the moment
	// there is one: it is what the user actually receives.
	if tx.Type == models.TransactionTypeOfframp {
		if amount := order.Amounts["destination_amount"]; amount != "" {
			tx.AmountFiat = amount
		}
		if amount := order.Amounts["source_amount"]; amount != "" {
			tx.AmountToken = amount
		}
	}
	// A bank payout's failure reason is the provider's, however the payout
	// was read: the webhook used to be the only path that kept it, so a
	// failure seen by a refresh or the sweep left the column empty. Other
	// rails keep their own bookkeeping of it.
	if isBankRail(tx) && order.FailureReason != "" {
		reason := order.FailureReason
		tx.FailureReason = &reason
	}
	if tx.Type == models.TransactionTypeOnramp {
		if amount := order.Amounts["destination_amount"]; amount != "" {
			tx.AmountToken = amount
		}
		if amount := order.Amounts["source_amount"]; amount != "" {
			tx.AmountFiat = amount
		}
	}
}

// isBankRail reports whether the row is a bank payout (ACH or wire).
func isBankRail(tx *models.Transaction) bool {
	return tx.Rail != nil && slices.Contains(models.BankRails(), *tx.Rail)
}

// fiatScale2 spells a decimal at the two places fiat_amount keeps. A value that
// is not a number is refused rather than written.
func fiatScale2(amount string) (string, bool) {
	value, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok {
		return "", false
	}
	return value.FloatString(2), true
}
