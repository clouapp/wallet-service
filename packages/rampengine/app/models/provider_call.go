package models

import "github.com/goravel/framework/database/orm"

// How a provider call, or a decision not to make one, ended.
const (
	// ProviderCallOK is a call the provider answered.
	ProviderCallOK = "ok"
	// ProviderCallFailed is a call the provider refused or could not answer.
	ProviderCallFailed = "failed"
	// ProviderCallSkipped is a call this side decided not to make. It is the
	// outcome the whole table exists for: a submission that never leaves looks
	// exactly like one that was never attempted, and only this says which.
	ProviderCallSkipped = "skipped"
)

// AllProviderCallOutcomes lists every outcome, in the order a reader should
// see them offered. It lives beside the constants so a new one reaches every
// consumer from one edit.
var AllProviderCallOutcomes = []string{
	ProviderCallOK, ProviderCallFailed, ProviderCallSkipped,
}

// ProviderCall is one line of a customer's provider timeline: something XIP
// asked a provider for it, or decided not to ask, and which user it was.
//
// **Metadata only, and that is a rule.** No request body, no response body, no
// provider customer id, no email, no document image, no amount tied to an
// identity — the same rule adapters/ramp/telemetry.go states for the
// structured logs.
// The question this answers is "did it happen, and what came back", and a
// payload copy answers nothing worth keeping a second copy of someone's
// identity documents to learn. A `detail` column is the shape that arrives
// carrying all of it at once; there is a test asserting this type's fields
// precisely so adding one is a deliberate act rather than a convenience.
//
// It has no soft deletes. A row here is a fact about the past, and a fact
// nobody may edit is one nobody needs to un-delete; retention prunes by age
// instead.
type ProviderCall struct {
	orm.Model
	// UserID is who made the call; CustomerID whom it was made for — the
	// user's own customer, or a company they act for.
	UserID     uint   `gorm:"not null;index" json:"user_id"`
	CustomerID uint   `gorm:"not null;index" json:"customer_id"`
	Provider   string `gorm:"size:32;not null" json:"provider"`
	// Operation is this side's name for the act — "submit_application",
	// "submit_documents", "start_verification" — never the provider's path.
	// The same act is a different path at each provider, and the reader is
	// comparing acts.
	Operation string `gorm:"size:48;not null" json:"operation"`
	Outcome   string `gorm:"size:16;not null" json:"outcome"`
	// ErrorCode is the stable contract code (ramp.ErrorCode) on a failure, or
	// the reason on a skip. Never the upstream message.
	ErrorCode *string `gorm:"size:64" json:"error_code,omitempty"`
	LatencyMs *uint   `json:"latency_ms,omitempty"`
	// SubmissionID groups the calls of one attempt. Absent on an admin action
	// or a refresh, which belong to no attempt.
	SubmissionID *string `gorm:"size:64" json:"submission_id,omitempty"`
	// RequestID is the provider's own identifier for the call — the first
	// thing their support asks for, and what previously had to be recovered
	// from server logs that may already have been pruned.
	RequestID *string `gorm:"size:64" json:"request_id,omitempty"`
}
