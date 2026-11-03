package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// MfaCredential is a subject's TOTP enrollment: the encrypted shared secret,
// when it was confirmed, and the last window it spent.
//
// A subject with no row has no MFA. A row whose ConfirmedAt is nil is a
// half-finished enrollment — the secret exists and the QR code has been shown,
// but the first code has not been proven yet, so it must not gate anything.
type MfaCredential struct {
	orm.Model
	SubjectType string     `gorm:"not null" json:"subject_type"`
	SubjectID   uint       `gorm:"not null" json:"subject_id"`
	Secret      string     `gorm:"not null" json:"-"`
	ConfirmedAt *time.Time `gorm:"column:confirmed_at" json:"confirmed_at,omitempty"`
	// LastUsedCounter is the last TOTP time-step this subject redeemed. A code
	// from that window or earlier is refused even if it is still arithmetically
	// valid, which is what stops a shoulder-surfed code from being replayed.
	LastUsedCounter int64 `gorm:"not null;default:0" json:"-"`
}

// Enrolled reports whether MFA is active for the subject.
//
// It is derived rather than stored: an `enabled` column alongside ConfirmedAt
// would be two facts that must agree, and the first code path that sets one
// without the other produces an account that is half-protected.
func (c *MfaCredential) Enrolled() bool {
	return c != nil && c.ConfirmedAt != nil
}
