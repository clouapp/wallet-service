package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WhitelistEntry struct {
	orm.Model
	ID       uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	WalletID uuid.UUID `gorm:"type:uuid;not null;index" json:"wallet_id"`
	Label    string    `gorm:"type:varchar(255)" json:"label,omitempty"`
	Address  string    `gorm:"type:text;not null" json:"address"`
}

func (w *WhitelistEntry) TableName() string { return "whitelist_entries" }
