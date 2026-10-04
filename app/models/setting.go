package models

import (
	"time"

	"github.com/google/uuid"
)

// Setting is one persisted configuration value. Type, label, help, default
// and the secret flag live in the settings registry, not in this row.
// Secret values are sealed ciphertext; this struct never decides that.
type Setting struct {
	ID        uint64     `gorm:"column:id;primaryKey"`
	AccountID *uuid.UUID `gorm:"column:account_id"`
	Group     string     `gorm:"column:group"`
	Key       string     `gorm:"column:key"`
	Value     string     `gorm:"column:value"`
	CreatedAt time.Time  `gorm:"column:created_at"`
	UpdatedAt time.Time  `gorm:"column:updated_at"`
}

func (Setting) TableName() string { return "settings" }
