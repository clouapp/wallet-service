package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletUTXO struct {
	orm.Model
	ID             uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	WalletID       uuid.UUID  `gorm:"type:uuid;not null;index" json:"wallet_id"`
	AddressID      *uuid.UUID `gorm:"type:uuid;index" json:"address_id,omitempty"`
	ChainID        string     `gorm:"type:varchar(32);not null;index" json:"chain_id"`
	TxHash         string     `gorm:"type:varchar(255);not null" json:"tx_hash"`
	OutputIndex    int        `gorm:"type:int;not null" json:"output_index"`
	Address        string     `gorm:"type:text;not null" json:"address"`
	ValueRaw       string     `gorm:"type:text;not null" json:"value_raw"`
	ScriptPubKey   *string    `gorm:"type:text" json:"script_pub_key,omitempty"`
	Status         string     `gorm:"type:utxo_status;not null" json:"status"`
	SpentByTxHash  *string    `gorm:"type:varchar(255)" json:"spent_by_tx_hash,omitempty"`
	BlockNumber    *int64     `gorm:"type:bigint" json:"block_number,omitempty"`
	BlockTimestamp *time.Time `gorm:"type:timestamptz" json:"block_timestamp,omitempty"`
	Confirmations  int        `gorm:"type:int;not null;default:0" json:"confirmations"`
	LastSyncedAt   time.Time  `gorm:"type:timestamptz;not null" json:"last_synced_at"`

	Wallet      *Wallet  `gorm:"foreignKey:WalletID" json:"wallet,omitempty"`
	AddressRef  *Address `gorm:"foreignKey:AddressID" json:"address_ref,omitempty"`
}

func (w *WalletUTXO) TableName() string {
	return "wallet_utxos"
}
