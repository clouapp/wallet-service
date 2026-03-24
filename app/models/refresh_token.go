package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type RefreshToken struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	TokenHash string     `gorm:"type:text;not null" json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

func (r *RefreshToken) TableName() string { return "refresh_tokens" }
