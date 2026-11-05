package ramp

import (
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// OrderChangedFromProvider reports whether a fresh provider order would change
// the persisted row after ApplyOrder runs.
func OrderChangedFromProvider(tx *models.Transaction, order *ramp.RampOrder) bool {
	scratch := *tx
	ApplyOrder(&scratch, order)
	return orderSnapshotChanged(tx, &scratch)
}

// DepositChangedFromProvider reports whether a fresh deposit activity would
// change the persisted row after ApplyDeposit runs.
func DepositChangedFromProvider(
	tx *models.Transaction,
	account *models.CustomerVirtualAccount,
	deposit *ramp.VirtualAccountDeposit,
) bool {
	scratch := *tx
	ApplyDeposit(&scratch, account, deposit)
	return orderSnapshotChanged(tx, &scratch)
}

// orderSnapshotChanged compares settlement-relevant columns semantically.
//
// RawPayload is deliberately excluded: Postgres canonicalizes jsonb, and the
// normalized columns already carry what the client reads.
func orderSnapshotChanged(before, after *models.Transaction) bool {
	if before.Status != after.Status {
		return true
	}
	if !decimalEqual(before.AmountFiat, after.AmountFiat) {
		return true
	}
	if !decimalEqual(before.AmountToken, after.AmountToken) {
		return true
	}
	if !ptrDecimalEqual(before.ExchangeRate, after.ExchangeRate) {
		return true
	}
	if !ptrDecimalEqual(before.ExchangeFeeAmount, after.ExchangeFeeAmount) {
		return true
	}
	if !ptrDecimalEqual(before.FeeAmount, after.FeeAmount) {
		return true
	}
	if !ptrStringEqual(before.FeeCurrency, after.FeeCurrency) {
		return true
	}
	if !ptrStringEqual(before.DepositTxSignature, after.DepositTxSignature) {
		return true
	}
	if !jsonStringEqual(before.DepositInstructions, after.DepositInstructions) {
		return true
	}
	if !timePtrEqual(before.ExpiresAt, after.ExpiresAt) {
		return true
	}
	if !timePtrEqual(before.CompletedAt, after.CompletedAt) {
		return true
	}
	if !ptrStringEqual(before.FailureReason, after.FailureReason) {
		return true
	}
	if !ptrStringEqual(before.SenderName, after.SenderName) {
		return true
	}
	if !ptrStringEqual(before.SenderReference, after.SenderReference) {
		return true
	}
	return false
}

// deref reads an optional column, an absent one as its zero value.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func ptrStringEqual(a, b *string) bool { return deref(a) == deref(b) }

func ptrDecimalEqual(a, b *string) bool { return decimalEqual(deref(a), deref(b)) }

func jsonStringEqual(a, b *string) bool {
	left, right := deref(a), deref(b)
	if left == right {
		return true
	}
	if left == "" || right == "" {
		return left == right
	}
	var leftValue, rightValue any
	if err := json.Unmarshal([]byte(left), &leftValue); err != nil {
		return left == right
	}
	if err := json.Unmarshal([]byte(right), &rightValue); err != nil {
		return left == right
	}
	leftJSON, errLeft := json.Marshal(leftValue)
	rightJSON, errRight := json.Marshal(rightValue)
	if errLeft != nil || errRight != nil {
		return left == right
	}
	return string(leftJSON) == string(rightJSON)
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

// decimalEqual compares two decimal strings by value.
//
// `1.5`, `1.50` and `1.500000` are one amount written three ways, and providers
// do not agree on which. Comparing them as strings would report a change on
// every run — the exact opposite of the idempotence the reconciliation sweep
// and the virtual-account fee push both promise. Anything that is not a number
// falls back to an exact string comparison, so a malformed value reads as
// divergent rather than silently equal.
func decimalEqual(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == b {
		return true
	}
	ra, okA := new(big.Rat).SetString(a)
	rb, okB := new(big.Rat).SetString(b)
	if !okA || !okB {
		return a == b
	}
	return ra.Cmp(rb) == 0
}
