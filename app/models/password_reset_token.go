package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type PasswordResetToken struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null"`
	TokenHash string     `gorm:"type:text;not null"`
	ExpiresAt time.Time  `gorm:"type:timestamptz;not null"`
	UsedAt    *time.Time `gorm:"type:timestamptz"`
}

func (p *PasswordResetToken) TableName() string { return "password_reset_tokens" }
