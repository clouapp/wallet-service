package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// Funding authorization states.
const (
	FundingAuthPrepared      = "prepared"
	FundingAuthFinalizing    = "finalizing"
	FundingAuthFinalized     = "finalized"
	FundingAuthFailed        = "failed"
	FundingAuthUnknown       = "unknown"
	FundingAuthNeedsOperator = "needs_operator"

	// FundingFailureQuoteExpired is the failure an authorization records when
	// its quote lapsed before the wallet funded it.
	FundingFailureQuoteExpired = "quote_expired"
	// FundingFailurePayoutNotCreated is the failure of a finalize whose payout
	// the provider provably never created: the quote lapsed and the listing
	// answered without it.
	FundingFailurePayoutNotCreated = "payout_not_created"
)

// OfframpFundingAuthorization is the funding action a provider prepared for an
// off-ramp transaction. Both ciphertext columns are encrypted at rest and never
// serialized. ActionRevision counts replacements of the unsigned action, which
// is what a compare-and-swap write keys on.
type OfframpFundingAuthorization struct {
	orm.Model
	ExternalID                string    `gorm:"type:uuid;not null" json:"-"`
	TransactionID             uint      `gorm:"not null" json:"-"`
	Provider                  string    `gorm:"size:32;not null" json:"-"`
	ActionType                string    `gorm:"size:32;not null" json:"-"`
	SubmissionMode            string    `gorm:"size:32;not null" json:"-"`
	ProviderStateCiphertext   string    `gorm:"type:text;not null" json:"-"`
	ActionPayloadCiphertext   string    `gorm:"type:text;not null" json:"-"`
	UnsignedActionFingerprint string    `gorm:"size:64;not null" json:"-"`
	ExpiresAt                 time.Time `gorm:"not null" json:"-"`
	ActionRevision            int       `gorm:"not null;default:1" json:"-"`
	State                     string    `gorm:"size:16;not null" json:"-"`
	FailureCode               *string   `gorm:"size:64" json:"-"`

	// The finalize intent: hashed client key, request hash, the provider's
	// idempotency key, the lease of whoever sends or recovers, and the signed
	// bytes (encrypted) kept until SignedPayloadRetainUntil.
	FinalizeClientKeyHash    *string    `gorm:"size:64" json:"-"`
	FinalizeRequestHash      *string    `gorm:"size:64" json:"-"`
	ProviderFinalizeKey      *string    `gorm:"size:255" json:"-"`
	FinalizeStartedAt        *time.Time `json:"-"`
	FinalizeLeaseExpiresAt   *time.Time `json:"-"`
	SignedPayloadCiphertext  *string    `gorm:"type:text" json:"-"`
	SignedPayloadRetainUntil *time.Time `json:"-"`

	// Recovery bookkeeping and the JSON payout binding (not secret: ids,
	// network, token, amounts and the owner address).
	RecoveryAttempts      int        `gorm:"not null;default:0" json:"-"`
	RecoveryNextAttemptAt *time.Time `json:"-"`
	PayoutBinding         *string    `gorm:"type:text" json:"-"`
}
