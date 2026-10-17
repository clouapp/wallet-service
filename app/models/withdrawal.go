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

// Failure reasons of a withdrawal whose payment left the service and failed on
// chain. A payment a validated ledger (XRP) or a block (EVM) includes with a
// failure result is WithdrawalFailureOnChainPrefix + its lowercase result code,
// e.g. onchain_tecno_dst_insuf_xrp, onchain_out_of_gas or onchain_reverted; an
// XRP payment that can no longer be included is WithdrawalFailureExpired.
const (
	WithdrawalFailureOnChainPrefix = "onchain_"
	WithdrawalFailureExpired       = "expired"
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
	// DestinationTag is the XRP Ledger destination tag; nil means none.
	DestinationTag *uint32    `gorm:"type:bigint"`
	FeeEstimate    string     `gorm:"type:decimal(36,18)"`
	Note           string     `gorm:"type:text"`
	CreatedBy      *uuid.UUID `gorm:"type:uuid"`
	// FailureReason holds the public error code returned to the caller, never internal details.
	FailureReason *string `gorm:"type:varchar(64)"`
	TxHash        string  `gorm:"-"`
}

func (w *Withdrawal) TableName() string { return "withdrawals" }
