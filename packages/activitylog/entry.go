package activitylog

import "time"

// Entry is one row of the trail: one STATEMENT's worth of change, not one row's.
//
// The distinction is not pedantry. gorm batches a slice Create into a single
// INSERT and therefore a single callback, and a DELETE with a predicate is one
// statement over N rows — so a design that assumed one entry per affected row
// would have to invent callbacks that never fire. See
// docs/specs/2026-08-27-activity-log-design.md §5.7.
type Entry struct {
	// LogName is the channel, in spatie's sense. Empty defaults to "default".
	LogName string
	// Scope is the host application's publisher scoping dimension, uninterpreted here.
	// Empty means the platform.
	Scope string
	// Event is created | updated | deleted, or an intent the caller named — a
	// users UPDATE that is really a sign-in looks like any other from the
	// statement alone.
	Event       string
	Description string

	SubjectType string
	// SubjectID is the affected row's id, or "" when the statement touched more
	// than one — in which case Properties carries "subjectIds".
	SubjectID string

	CauserType string
	CauserID   string
	// CauserLabel is the actor's address AT THE TIME, snapshotted. Never
	// resolved later from a foreign key: a record a user deletion can erase is
	// not an audit record.
	CauserLabel string

	// Properties holds the before/after images, already redacted. It never holds
	// the statement's WHERE clause or its arguments — those carry literals like
	// an email address or a TOTP step. See §6.3.
	Properties map[string]any

	// BatchUUID groups the statements of one request, so a single Save that
	// produced a DELETE and an INSERT reads as one act.
	BatchUUID string

	// CreatedAt defaults to now when zero.
	CreatedAt time.Time
}
