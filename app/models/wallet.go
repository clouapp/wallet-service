package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Wallet struct {
	orm.Model
	ID             uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	Chain          string    `gorm:"type:varchar(50);not null;index" json:"chain"`
	Label          string    `gorm:"type:varchar(255)" json:"label"`
	MasterPubkey   string    `gorm:"type:text;not null" json:"-"`
	KeyVaultRef    string    `gorm:"type:varchar(255);not null" json:"-"`
	DerivationPath string    `gorm:"type:varchar(100);not null" json:"derivation_path"`
	AddressIndex   int       `gorm:"type:int;not null;default:0" json:"address_index"`
}

// TableName specifies the table name for Wallet model
func (w *Wallet) TableName() string {
	return "wallets"
}
