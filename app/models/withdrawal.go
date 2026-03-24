package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type Withdrawal struct {
	orm.Model
	ID                 uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	WalletID           uuid.UUID  `gorm:"type:uuid;not null;index" json:"wallet_id"`
	TransactionID      *uuid.UUID `gorm:"type:uuid" json:"transaction_id,omitempty"`
	AccountID          *uuid.UUID `gorm:"type:uuid;index" json:"account_id,omitempty"`
	Status             string     `gorm:"type:varchar(20);default:pending" json:"status"`
	Amount             string     `gorm:"type:decimal(36,18)" json:"amount"`
	DestinationAddress string     `gorm:"type:text;not null" json:"destination_address"`
	FeeEstimate        string     `gorm:"type:decimal(36,18)" json:"fee_estimate,omitempty"`
	Note               string     `gorm:"type:text" json:"note,omitempty"`
	CreatedBy          *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
}

func (w *Withdrawal) TableName() string { return "withdrawals" }
