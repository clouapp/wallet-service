package ramp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

const (
	// FundingActionSolanaTransaction is the one action type the contract
	// carries: a Solana transaction the wallet signs.
	FundingActionSolanaTransaction = "solana_transaction"
	// SubmissionBackendFinalize means the wallet returns the signed
	// transaction to the backend, which submits it.
	SubmissionBackendFinalize = "backend_finalize"
	// ContentTypeSolanaTransaction is the content type of the unsigned payload.
	ContentTypeSolanaTransaction = "application/x.solana-transaction"
	// MaxUnsignedTransactionBytes bounds the decoded unsigned transaction.
	MaxUnsignedTransactionBytes = 4096
)

// FundingActionPayload is the unsigned transaction the wallet signs.
type FundingActionPayload struct {
	ContentType         string
	UnsignedTransaction string // base64 (std), at most MaxUnsignedTransactionBytes decoded
}

// FundingAction is what the wallet must do to fund a prepared off-ramp.
type FundingAction struct {
	Type           string
	SubmissionMode string
	Payload        FundingActionPayload
	ExpiresAt      time.Time
}

// Validate refuses an action the contract does not carry: another type or
// mode, another content type, empty or oversized or non-base64 payload, zero
// expiry.
func (a FundingAction) Validate() error {
	if a.Type != FundingActionSolanaTransaction {
		return fmt.Errorf("ramp: funding action type %q is not supported", a.Type)
	}
	if a.SubmissionMode != SubmissionBackendFinalize {
		return fmt.Errorf("ramp: funding action submission mode %q is not supported", a.SubmissionMode)
	}
	if a.Payload.ContentType != ContentTypeSolanaTransaction {
		return fmt.Errorf("ramp: funding action content type %q is not supported", a.Payload.ContentType)
	}
	if a.Payload.UnsignedTransaction == "" {
		return errors.New("ramp: funding action carries no transaction")
	}
	raw, err := base64.StdEncoding.DecodeString(a.Payload.UnsignedTransaction)
	if err != nil {
		return fmt.Errorf("ramp: funding action transaction is not base64: %w", err)
	}
	if len(raw) == 0 {
		return errors.New("ramp: funding action carries no transaction")
	}
	if len(raw) > MaxUnsignedTransactionBytes {
		return fmt.Errorf("ramp: funding action transaction exceeds %d bytes", MaxUnsignedTransactionBytes)
	}
	if a.ExpiresAt.IsZero() {
		return errors.New("ramp: funding action has no expiry")
	}
	return nil
}

// OfframpPrepareRequest asks a provider to prepare the funding of a quoted
// off-ramp.
type OfframpPrepareRequest struct {
	ProviderQuoteID string
	OwnerAddress    string
	Token           string // client vocabulary (USDC/USDT)
	Chain           string
	SourceAmount    string // the quote's own spelling
	QuoteExpiresAt  time.Time
}

// PreparedOfframpFunding is a provider's answer to a prepare.
type PreparedOfframpFunding struct {
	Action FundingAction
	// CorrelationState is the provider's own record of what was prepared,
	// opaque to the service, stored encrypted and handed back to the validator.
	CorrelationState []byte
	// MessageFingerprint is the SHA-256 hex of the message bytes to be signed.
	MessageFingerprint string
}

// OfframpPrepareAdapter is a provider that prepares the funding of an off-ramp
// before the order exists. Optional, found by type assertion like
// BankAccountRegistrar.
type OfframpPrepareAdapter interface {
	PrepareOfframp(ctx context.Context, ref CustomerRef, req OfframpPrepareRequest) (*PreparedOfframpFunding, error)
}

// SignedOfframpActionValidationRequest is the wallet's signed answer plus what
// the prepare recorded.
type SignedOfframpActionValidationRequest struct {
	SignedTransaction  string // base64
	MessageFingerprint string
	CorrelationState   []byte
}

// SignedOfframpActionValidator checks a signed funding action against what was
// prepared. Optional, found by type assertion.
//
// ValidateSignedOfframpAction: only ErrInvalidSignedActionPayload is a
// business refusal; any other error is retryable.
type SignedOfframpActionValidator interface {
	ValidateSignedOfframpAction(ctx context.Context, ref CustomerRef, req SignedOfframpActionValidationRequest) error
}
