package models

import (
	"time"

	"github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/identity"
)

// OTPCode is a single-use numeric code issued to any authenticatable subject —
// a user or an admin — for email verification, password reset, or login.
//
// It replaces the former `email_verifications` and `admin_password_resets`,
// which were the same six columns written twice.
type OTPCode struct {
	orm.Model
	SubjectType string              `gorm:"not null;index" json:"subject_type"`
	SubjectID   uint                `gorm:"not null;index" json:"subject_id"`
	Purpose     identity.OTPPurpose `gorm:"not null;index" json:"purpose"`
	Code        string              `gorm:"not null" json:"-"`
	ExpiresAt   time.Time           `gorm:"not null" json:"expires_at"`
	UsedAt      *time.Time          `gorm:"column:used_at" json:"used_at"`
}
