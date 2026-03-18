package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Transaction struct {
	orm.Model
	ID             uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	AddressID      *uuid.UUID `gorm:"type:uuid;index" json:"address_id"`
	WalletID       uuid.UUID  `gorm:"type:uuid;not null;index" json:"wallet_id"`
	ExternalUserID string     `gorm:"type:varchar(255);not null;index" json:"external_user_id"`
	Chain          string     `gorm:"type:varchar(50);not null;index" json:"chain"`
	TxType         string     `gorm:"type:varchar(20);not null;index" json:"tx_type"`
	TxHash         string     `gorm:"type:varchar(255);index" json:"tx_hash"`
	FromAddress    string     `gorm:"type:varchar(255)" json:"from_address"`
	ToAddress      string     `gorm:"type:varchar(255);not null" json:"to_address"`
	Amount         string     `gorm:"type:varchar(100);not null" json:"amount"`
	Asset          string     `gorm:"type:varchar(50);not null" json:"asset"`
	TokenContract  string     `gorm:"type:varchar(255)" json:"token_contract"`
	Confirmations  int        `gorm:"type:int;not null;default:0" json:"confirmations"`
	RequiredConfs  int        `gorm:"type:int;not null;default:12" json:"required_confs"`
	Status         string     `gorm:"type:varchar(20);not null;index" json:"status"`
	Fee            string     `gorm:"type:varchar(100)" json:"fee"`
	BlockNumber    int64      `gorm:"type:bigint;index:idx_chain_block" json:"block_number"`
	BlockHash      string     `gorm:"type:varchar(255)" json:"block_hash"`
	ErrorMessage   string     `gorm:"type:text" json:"error_message"`
	IdempotencyKey string     `gorm:"type:varchar(255);unique" json:"idempotency_key"`
	ConfirmedAt    *time.Time `gorm:"type:timestamp" json:"confirmed_at"`

	// Relationships
	Address *Address `gorm:"foreignKey:AddressID" json:"address,omitempty"`
	Wallet  *Wallet  `gorm:"foreignKey:WalletID" json:"wallet,omitempty"`
}

// TableName specifies the table name for Transaction model
func (t *Transaction) TableName() string {
	return "transactions"
}
