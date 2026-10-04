package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WhitelistEntry struct {
	orm.Model
	ID       uuid.UUID `gorm:"type:uuid;primary_key"`
	WalletID uuid.UUID `gorm:"type:uuid;not null;index"`
	Label    string    `gorm:"type:varchar(255)"`
	Address  string    `gorm:"type:text;not null"`
}

func (w *WhitelistEntry) TableName() string { return "whitelist_entries" }
