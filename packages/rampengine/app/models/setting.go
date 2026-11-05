package models

import "github.com/goravel/framework/database/orm"

// Setting is one persisted configuration value addressed by (group, key).
// Metadata (type, label, default, secret) lives in app/settings registry, not
// here — this table stores only values. Secret values are stored encrypted.
type Setting struct {
	orm.Model
	orm.SoftDeletes
	Group string `gorm:"not null" json:"group"`
	Key   string `gorm:"not null" json:"key"`
	Value string `gorm:"type:text" json:"value"`
}
