package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

const (
	WithdrawalStatusBroadcasting = "broadcasting"
	WithdrawalStatusBroadcast    = "broadcast"
	WithdrawalStatusConfirmed    = "confirmed"
	WithdrawalStatusFailed       = "failed"
)

type Withdrawal struct {
	orm.Model
	ID                 uuid.UUID  `gorm:"type:uuid;primary_key"`
	WalletID           uuid.UUID  `gorm:"type:uuid;not null;index"`
	TransactionID      *uuid.UUID `gorm:"type:uuid"`
	AccountID          *uuid.UUID `gorm:"type:uuid;index"`
	Status             string     `gorm:"type:varchar(20);default:pending"`
	Amount             string     `gorm:"type:decimal(36,18)"`
	DestinationAddress string     `gorm:"type:text;not null"`
	FeeEstimate        string     `gorm:"type:decimal(36,18)"`
	Note               string     `gorm:"type:text"`
	CreatedBy          *uuid.UUID `gorm:"type:uuid"`
	// FailureReason holds the public error code returned to the caller, never internal details.
	FailureReason *string `gorm:"type:varchar(64)"`
	TxHash        string  `gorm:"-"`
}

func (w *Withdrawal) TableName() string { return "withdrawals" }
