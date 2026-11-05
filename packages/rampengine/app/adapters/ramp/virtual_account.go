package ramp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// VirtualAccountAdapter is a provider that collects fiat through a standing
// deposit account rather than a per-order instruction. It is deliberately not
// part of ProviderAdapter: only Bridge serves this shape today, and folding it
// into the fat interface would make both stubs implement a refusal they will
// never be asked for.
//
// The service layer type-asserts it off a resolved ProviderAdapter; a provider
// that does not implement it simply cannot serve the rail this way.
type VirtualAccountAdapter interface {
	Code() ProviderCode
	// ProvisionVirtualAccount returns the customer's standing account for the
	// currency, creating one only when none exists.
	//
	// An implementation must not assume the provider deduplicates. Bridge
	// documents one active account per customer per currency and does not
	// enforce it: a repeated create under a fresh idempotency key returns a
	// second account with its own deposit instructions. Two accounts means two
	// deposit addresses for one user, and money paid into the one we stopped
	// tracking arrives with no order to attach it to.
	ProvisionVirtualAccount(ctx context.Context, ref CustomerRef, req VirtualAccountRequest) (*VirtualAccount, error)
	GetVirtualAccount(ctx context.Context, ref CustomerRef, accountID string) (*VirtualAccount, error)
	// UpdateVirtualAccount changes the destination or the activation status of
	// an existing account. The deposit instructions must survive it: the user
	// has the br_code saved in their bank app.
	UpdateVirtualAccount(ctx context.Context, ref CustomerRef, accountID string, req VirtualAccountUpdate) (*VirtualAccount, error)
	// GetDeposit re-reads one deposit out of the account's history. It is the
	// reconciliation path for this rail: a deposit is not a transfer and
	// GetOrder's /transfers/{id} would 404 on a deposit id.
	GetDeposit(ctx context.Context, ref CustomerRef, accountID, depositID string) (*VirtualAccountDeposit, error)
}

// StandingAccount is adapter's standing-account port. A provider that does not
// collect this way is refused with ErrStandingAccountUnsupported wrapped, which
// the router fails over on and the client controllers map with errors.Is.
func StandingAccount(adapter ProviderAdapter) (VirtualAccountAdapter, error) {
	standing, ok := adapter.(VirtualAccountAdapter)
	if !ok {
		return nil, fmt.Errorf("provider %q: %w", adapter.Code(), ErrStandingAccountUnsupported)
	}
	return standing, nil
}

// VirtualAccountRequest provisions a standing deposit account.
type VirtualAccountRequest struct {
	// SourceCurrency is the fiat collected (`brl`).
	SourceCurrency string
	Destination    CryptoLeg
	// FeePercent is XIP's cut for the rail. Unlike a transfer, it is a property
	// of the account and applies to every deposit until the account is updated.
	FeePercent string
	// IdempotencyKey is derived by the service from a value that survives a
	// retry; the adapter only forwards it upstream.
	IdempotencyKey string
}

// VirtualAccountUpdate changes an existing account. Empty fields are left
// alone; Active is a pointer so "do not touch" is distinguishable from
// "deactivate".
type VirtualAccountUpdate struct {
	Destination *CryptoLeg
	FeePercent  string
	Active      *bool
}

// VirtualAccount is a standing deposit account, normalized.
type VirtualAccount struct {
	ProviderAccountID string
	// Status is `active` or `inactive`, canonical.
	Status string
	// DepositInstructions is what the user pays into — for BRL/PIX the
	// `br_code` and the account holder name. It is permanent: it must be shown
	// as a saved payment target, not as a per-order QR.
	DepositInstructions map[string]any
	Destination         CryptoLeg
	SourceCurrency      string
	FeePercent          string
	Raw                 json.RawMessage
}

// VirtualAccountDeposit is one deposit's lifecycle state, whether it arrived on
// a webhook or was re-read from history.
type VirtualAccountDeposit struct {
	DepositID         string
	ProviderAccountID string
	// Status is a canonical transaction status.
	Status string
	// SourceAmount is the fiat that arrived, before any fee. It is read from
	// the receipt rather than from the event's own `amount`, which switches
	// legs: fiat on the funds event, crypto on the payment ones.
	SourceAmount   string
	SourceCurrency string
	// DestinationAmount is the crypto delivered, known only once the payment
	// leg reports a receipt.
	DestinationAmount string
	FeeAmount         string
	// FeeCurrency is the fiat currency of the deposit — the fee comes off the
	// BRL before conversion, so reporting it in the token would misstate it.
	FeeCurrency       string
	ExchangeFeeAmount string
	TxHash            string
	FailureReason     string
	// SenderName is who paid. It is the only handle an operator has on a
	// third-party deposit, and it is PII: persisted, never logged.
	//
	// There is no `sender_name` field: the documentation names one, the wire
	// does not have it. `source` is rail-shaped — a wire deposit carries
	// `originator_name`, and PIX will carry something else again. The adapter
	// reads the rail first and picks the field; anything it cannot name leaves
	// this empty rather than guessing.
	SenderName      string
	SenderReference string
	// PaymentRail is the bank network the money arrived on, normalized:
	// "ach", "wire", "fednow" or "pix"; "" when the provider names none this
	// side knows.
	PaymentRail string
	OccurredAt  *time.Time
	// Raw is the untouched payload. It carries payer PII and is never logged.
	Raw json.RawMessage
}
