package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Address struct {
	orm.Model
	ID              uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	WalletID        uuid.UUID `gorm:"type:uuid;not null;index" json:"wallet_id"`
	Chain           string    `gorm:"type:varchar(50);not null;index" json:"chain"`
	Address         string    `gorm:"type:varchar(255);not null;unique" json:"address"`
	DerivationIndex int       `gorm:"type:int;not null" json:"derivation_index"`
	ExternalUserID  string    `gorm:"type:varchar(255);not null;index" json:"external_user_id"`
	Metadata        string    `gorm:"type:text" json:"metadata"`
	IsActive        bool      `gorm:"type:boolean;not null;default:true;index" json:"is_active"`

	// Relationship
	Wallet *Wallet `gorm:"foreignKey:WalletID" json:"wallet,omitempty"`
}

// TableName specifies the table name for Address model
func (a *Address) TableName() string {
	return "addresses"
}
