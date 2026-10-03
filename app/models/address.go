package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Address struct {
	orm.Model
	ID                  uuid.UUID  `gorm:"type:uuid;primary_key"`
	WalletID            uuid.UUID  `gorm:"type:uuid;not null;index"`
	Chain               string     `gorm:"type:varchar(50);not null;index"`
	Address             string     `gorm:"type:varchar(255);not null;unique"`
	DerivationIndex     int        `gorm:"type:int;not null"`
	ExternalUserID      string     `gorm:"type:varchar(255);not null;index"`
	Metadata            string     `gorm:"type:text"`
	IsActive            bool       `gorm:"type:boolean;not null;default:true;index"`
	Label               string     `gorm:"type:varchar(255)"`
	CreatedBy           *uuid.UUID `gorm:"type:uuid"`
	DerivationType      string     `gorm:"type:varchar(20);not null;default:genesis"`
	EncryptedPrivateKey string     `gorm:"type:text"`
	EncryptionIV        string     `gorm:"type:text"`
	EncryptionSalt      string     `gorm:"type:text"`

	// Relationship
	Wallet *Wallet `gorm:"foreignKey:WalletID"`
}

// TableName specifies the table name for Address model
func (a *Address) TableName() string {
	return "addresses"
}
