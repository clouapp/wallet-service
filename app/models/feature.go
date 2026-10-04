package models

import (
	"time"

	"github.com/google/uuid"
)

// Feature is one account switch. The catalog owns the name, the label and
// the default. This row stores only the boolean an owner or admin wrote.
// It never holds a secret.
type Feature struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	AccountID uuid.UUID `gorm:"column:account_id"`
	Key       string    `gorm:"column:key"`
	Enabled   bool      `gorm:"column:enabled"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (Feature) TableName() string { return "features" }
