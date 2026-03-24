package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type PasswordResetToken struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null" json:"user_id"`
	TokenHash string     `gorm:"type:text;not null" json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
}

func (p *PasswordResetToken) TableName() string { return "password_reset_tokens" }
