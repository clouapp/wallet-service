package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type RefreshToken struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null;index"`
	TokenHash string     `gorm:"type:text;not null"`
	ExpiresAt time.Time  `gorm:"type:timestamptz;not null"`
	RevokedAt *time.Time `gorm:"type:timestamptz"`
}

func (r *RefreshToken) TableName() string { return "refresh_tokens" }
