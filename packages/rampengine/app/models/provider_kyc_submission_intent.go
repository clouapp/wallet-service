package models

import (
	"encoding/json"
	"time"

	"github.com/goravel/framework/database/orm"
)

// Provider KYC submission intent states.
//
// An intent is the durable record of one atomic customer create: it outlives
// the staging blob, the job and the process, so a create whose outcome is
// unknown is replayed with the same bytes and the same idempotency key instead
// of being rebuilt.
const (
	KycIntentPreparing    = "preparing"
	KycIntentPrepared     = "prepared"
	KycIntentSubmitting   = "submitting"
	KycIntentUnknown      = "unknown"
	KycIntentCompleted    = "completed"
	KycIntentFailed       = "failed"
	KycIntentManualReview = "manual_review"
	KycIntentResolved     = "resolved"
)

// AllKycIntentStates lists every intent state; the migration's CHECK is
// written from the same names.
var AllKycIntentStates = []string{
	KycIntentPreparing, KycIntentPrepared, KycIntentSubmitting, KycIntentUnknown,
	KycIntentCompleted, KycIntentFailed, KycIntentManualReview, KycIntentResolved,
}

// ActiveKycIntentStates are the states that hold the (customer, provider) slot: at
// most one intent in them per pair. manual_review holds it too, so nothing
// creates a second customer while an operator is still deciding what the
// first attempt did.
var ActiveKycIntentStates = []string{
	KycIntentPreparing, KycIntentPrepared, KycIntentSubmitting, KycIntentUnknown, KycIntentManualReview,
}

// RecoverableKycIntentStates are the states the recovery sweep may claim.
var RecoverableKycIntentStates = []string{
	KycIntentPreparing, KycIntentPrepared, KycIntentSubmitting, KycIntentUnknown,
}

// How an operator closed an intent in manual review.
const (
	KycIntentOutcomeReconciled = "reconciled"
	KycIntentOutcomeRejected   = "rejected"
	KycIntentOutcomeCancelled  = "cancelled"
)

// KycIntentOutcomes lists every resolution outcome.
var KycIntentOutcomes = []string{KycIntentOutcomeReconciled, KycIntentOutcomeRejected, KycIntentOutcomeCancelled}

// ProviderKycSubmissionIntent is one atomic customer create at a provider.
//
// Every write goes through the repository's revision guard: the worker, the
// recovery sweep and an operator can all hold the same row, and the one that
// read it last must not undo what another wrote since.
type ProviderKycSubmissionIntent struct {
	orm.Model
	ExternalID   string `gorm:"type:uuid;not null" json:"external_id"`
	CustomerID   uint   `gorm:"not null" json:"customer_id"`
	Provider     string `gorm:"size:32;not null" json:"provider"`
	SubmissionID string `gorm:"size:64;not null" json:"submission_id"`
	State        string `gorm:"size:16;not null" json:"state"`
	// ProviderIdempotencyKey is sent with every create of this intent, so the
	// provider answers a replay with the customer the first one made.
	ProviderIdempotencyKey string `gorm:"size:128;not null" json:"-"`
	// MaterialCiphertext is the KycIntentMaterial, sealed with facades.Crypt().
	// It never carries images and is never logged.
	MaterialCiphertext string `gorm:"type:text;not null" json:"-"`
	// BodySHA256 is the digest of the prepared create body: present from
	// prepared on, so a replay can be proven to send the same bytes.
	BodySHA256             *string    `gorm:"column:body_sha256;size:64" json:"body_sha256,omitempty"`
	ProviderCustomerID     *string    `gorm:"size:64" json:"provider_customer_id,omitempty"`
	AttemptCount           int        `gorm:"not null;default:0" json:"attempt_count"`
	LastAttemptAt          *time.Time `json:"last_attempt_at,omitempty"`
	RetryDeadline          time.Time  `gorm:"not null" json:"retry_deadline"`
	RecoveryNextAttemptAt  *time.Time `json:"recovery_next_attempt_at,omitempty"`
	RecoveryLeaseExpiresAt *time.Time `json:"recovery_lease_expires_at,omitempty"`
	RecoveryAttempts       int        `gorm:"not null;default:0" json:"recovery_attempts"`
	FailureCode            *string    `gorm:"size:64" json:"failure_code,omitempty"`
	MaterialRetainUntil    *time.Time `json:"material_retain_until,omitempty"`
	MaterialClearedAt      *time.Time `json:"material_cleared_at,omitempty"`
	ResolvedAt             *time.Time `json:"resolved_at,omitempty"`
	ResolutionOutcome      *string    `gorm:"size:32" json:"resolution_outcome,omitempty"`
	ResolutionReason       *string    `gorm:"size:255" json:"resolution_reason,omitempty"`
	Revision               int        `gorm:"not null;default:1" json:"revision"`
}

// AtomicKycUploadedField is one uploaded file: which payload field it fills,
// the digest of what was uploaded (so a resumed run proves it is the same
// file), and the provider's handle for it.
//
// It and the two types below are declared here and aliased by
// app/adapters/ramp, for the reason models.KycProfile is: ramp imports
// app/repositories, so the intent repository could not name the ramp types
// without a build cycle. One declaration keeps the stored material and the
// port's types the same Go type, not two structs that must agree.
type AtomicKycUploadedField struct {
	Field          string `json:"field"`
	DocumentSHA256 string `json:"document_sha256"`
	ProviderHandle string `json:"provider_handle"`
}

// AtomicKycCheckpoint is what an interrupted prepare already uploaded.
type AtomicKycCheckpoint struct {
	Version      int                      `json:"version"`
	SubmissionID string                   `json:"submission_id"`
	Fields       []AtomicKycUploadedField `json:"fields,omitempty"`
}

// PreparedAtomicKyc is the exact create body and its digest. A retry sends
// these bytes, never a rebuilt body: the provider compares retries by the
// hash of the bytes.
type PreparedAtomicKyc struct {
	Version       int             `json:"version"`
	Payload       json.RawMessage `json:"payload"`
	PayloadSHA256 string          `json:"payload_sha256"`
}
