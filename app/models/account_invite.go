package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type AccountInvite struct {
	orm.Model
	ID         uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	AccountID  uuid.UUID  `gorm:"type:uuid;not null;index" json:"account_id"`
	Email      string     `gorm:"type:varchar(255);not null" json:"email"`
	Role       string     `gorm:"type:varchar(20);not null" json:"role"`
	TokenHash  string     `gorm:"type:text;not null" json:"-"`
	InvitedBy  *uuid.UUID `gorm:"type:uuid" json:"invited_by,omitempty"`
	ExpiresAt  time.Time  `gorm:"type:timestamptz;not null" json:"expires_at"`
	AcceptedAt *time.Time `gorm:"type:timestamptz" json:"accepted_at,omitempty"`
	RevokedAt  *time.Time `gorm:"type:timestamptz" json:"revoked_at,omitempty"`
}

func (a *AccountInvite) TableName() string { return "account_invites" }
