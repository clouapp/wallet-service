// Package authmodel is the user row the auth config and app/models share.
// Config may not import app/models; both may import pkg. Field tags, the
// users table name, and the preferences JSON encoding are unchanged.
package authmodel

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type User struct {
	orm.Model
	ID           uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	Email        string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"email"`
	PasswordHash string    `gorm:"type:text;not null" json:"-"`
	FullName     string    `gorm:"type:varchar(255)" json:"full_name,omitempty"`
	// TotpSecret is the sealed TOTP secret loaded from mfa_credentials. It is
	// not a users column and it is never part of an HTTP body.
	TotpSecret       string           `gorm:"-" json:"-"`
	TotpEnabled      bool             `gorm:"default:false" json:"totp_enabled"`
	Status           string           `gorm:"type:varchar(20);default:active" json:"status"`
	DefaultAccountID *uuid.UUID       `gorm:"type:uuid" json:"default_account_id,omitempty"`
	Preferences      *UserPreferences `gorm:"type:jsonb" json:"preferences,omitempty"`

	// TotpLastUsedCounter is the last TOTP time-step this user redeemed; a code
	// whose step is not newer is a replay.
	TotpLastUsedCounter int64 `gorm:"not null;default:0" json:"-"`
	// SessionsRevokedAt is the session watermark: sessions and 2FA challenges
	// issued before it are refused.
	SessionsRevokedAt *time.Time `gorm:"type:timestamptz" json:"-"`
	// SuspendedAt is the platform suspension. A set value refuses login,
	// refresh and the next session request. It is not a membership status.
	SuspendedAt *time.Time `gorm:"type:timestamptz" json:"-"`
	// SuspensionReason is stored only for operators. It is never returned
	// and never copied into activity metadata.
	SuspensionReason *string `gorm:"type:text" json:"-"`
}

func (u *User) TableName() string { return "users" }
