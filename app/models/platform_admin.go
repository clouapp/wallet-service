package models

import (
	"time"

	"github.com/google/uuid"
)

// PlatformAdmin is one dashboard user allowed to manage platform feature
// flags. The user id is the primary key, so a user has at most one row.
// The row holds no secret.
type PlatformAdmin struct {
	UserID    uuid.UUID `gorm:"column:user_id;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (PlatformAdmin) TableName() string { return "platform_admins" }
