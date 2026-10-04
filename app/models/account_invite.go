package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

// AccountInvite is a pending membership. The raw token is never stored.
type AccountInvite struct {
	orm.Model
	ID          uuid.UUID  `gorm:"type:uuid;primary_key"`
	AccountID   uuid.UUID  `gorm:"type:uuid;not null;index"`
	Email       string     `gorm:"type:varchar(255);not null"`
	Role        string     `gorm:"type:varchar(20);not null"`
	TokenHash   string     `gorm:"type:text;not null"`
	InvitedBy   *uuid.UUID `gorm:"type:uuid"`
	ExpiresAt   time.Time  `gorm:"type:timestamptz;not null"`
	AcceptedAt  *time.Time `gorm:"type:timestamptz"`
	RevokedAt   *time.Time `gorm:"type:timestamptz"`
	WalletRoles *string    `gorm:"type:jsonb"`
}

func (a *AccountInvite) TableName() string { return "account_invites" }
