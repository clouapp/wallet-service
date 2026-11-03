package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// MfaBackupCode is a single-use recovery code for a subject's TOTP MFA. The
// plaintext code is shown once at enrollment; only its bcrypt hash is stored.
type MfaBackupCode struct {
	orm.Model
	SubjectType string     `gorm:"not null" json:"-"`
	SubjectID   uint       `gorm:"not null" json:"-"`
	CodeHash    string     `gorm:"not null" json:"-"`
	UsedAt      *time.Time `gorm:"column:used_at" json:"-"`
}
