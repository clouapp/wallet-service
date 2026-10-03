package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletUTXO struct {
	orm.Model
	ID             uuid.UUID  `gorm:"type:uuid;primary_key"`
	WalletID       uuid.UUID  `gorm:"type:uuid;not null;index"`
	AddressID      *uuid.UUID `gorm:"type:uuid;index"`
	ChainID        string     `gorm:"type:varchar(32);not null;index"`
	TxHash         string     `gorm:"type:varchar(255);not null"`
	OutputIndex    int        `gorm:"type:int;not null"`
	Address        string     `gorm:"type:text;not null"`
	ValueRaw       string     `gorm:"type:text;not null"`
	ScriptPubKey   *string    `gorm:"type:text"`
	Status         string     `gorm:"type:utxo_status;not null"`
	SpentByTxHash  *string    `gorm:"type:varchar(255)"`
	BlockNumber    *int64     `gorm:"type:bigint"`
	BlockTimestamp *time.Time `gorm:"type:timestamptz"`
	Confirmations  int        `gorm:"type:int;not null;default:0"`
	LastSyncedAt   time.Time  `gorm:"type:timestamptz;not null"`

	Wallet     *Wallet  `gorm:"foreignKey:WalletID"`
	AddressRef *Address `gorm:"foreignKey:AddressID"`
}

func (w *WalletUTXO) TableName() string {
	return "wallet_utxos"
}
