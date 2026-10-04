package models

import "time"

// GlobalFeature is one platform switch. The catalog owns the name and the
// default. This row stores only the boolean a platform admin wrote. It never
// holds a secret. A missing row is not the same as enabled=false.
type GlobalFeature struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	Key       string    `gorm:"column:key"`
	Enabled   bool      `gorm:"column:enabled"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (GlobalFeature) TableName() string { return "global_features" }
