package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

const (
	TxOriginUserRequest         = "user_request"
	TxOriginSweep               = "sweep"
	TxOriginGasSeed             = "gas_seed"
	TxOriginManualConsolidation = "manual_consolidation"
)

const (
	TxDirectionInbound  = "inbound"
	TxDirectionOutbound = "outbound"
	TxDirectionSelf     = "self"
	TxDirectionUnknown  = "unknown"
)

const (
	TxSourceChain          = "chain"
	TxSourceDepositIngest  = "deposit_ingest"
	TxSourceWithdrawalFlow = "withdrawal_flow"
	TxSourceReconciliation = "reconciliation"
)

const (
	TxTypeDeposit       = "deposit"
	TxTypeWithdrawal    = "withdrawal"
	TxTypeTransfer      = "transfer"
	TxTypeTokenTransfer = "token_transfer"
	TxTypeFee           = "fee"
	TxTypeSweep         = "sweep"
	TxTypeGasSeed       = "gas_seed"
)

// ScannerDepositLogIndex is the log index of a block-scanner deposit, which is keyed
// by chain and transaction alone; the unique deposit index covers (chain, tx_hash,
// log_index), so one scanner row per transaction is enforced by the database.
const ScannerDepositLogIndex = -1

type Transaction struct {
	orm.Model
	ID                  uuid.UUID  `gorm:"type:uuid;primary_key"`
	AddressID           *uuid.UUID `gorm:"type:uuid;index"`
	WalletID            uuid.UUID  `gorm:"type:uuid;not null;index"`
	ExternalUserID      string     `gorm:"type:varchar(255);not null;index"`
	Chain               string     `gorm:"type:varchar(50);not null;index"`
	TxType              string     `gorm:"type:transaction_type;not null;index"`
	TxHash              string     `gorm:"type:varchar(255);index"`
	LogIndex            int        `gorm:"type:int;default:-1"`
	FromAddress         string     `gorm:"type:varchar(255)"`
	ToAddress           string     `gorm:"type:varchar(255);not null"`
	Amount              string     `gorm:"type:varchar(100);not null"`
	Asset               string     `gorm:"type:varchar(50);not null"`
	TokenContract       string     `gorm:"type:varchar(255)"`
	Confirmations       int        `gorm:"type:int;not null;default:0"`
	RequiredConfs       int        `gorm:"type:int;not null;default:12"`
	Status              string     `gorm:"type:transaction_status;not null;index"`
	Fee                 string     `gorm:"type:varchar(100)"`
	BlockNumber         int64      `gorm:"type:bigint;index:idx_chain_block"`
	BlockHash           string     `gorm:"type:varchar(255)"`
	ErrorMessage        string     `gorm:"type:text"`
	IdempotencyKey      *string    `gorm:"type:varchar(255)"`
	ConfirmedAt         *time.Time `gorm:"type:timestamptz"`
	Direction           string     `gorm:"type:transaction_direction"`
	Source              string     `gorm:"type:transaction_source"`
	ParentTransactionID *uuid.UUID `gorm:"type:uuid;index"`
	Origin              string     `gorm:"type:varchar(24);index"`
	RawPayload          string     `gorm:"type:jsonb"`
	SyncedAt            *time.Time `gorm:"type:timestamptz"`

	// Relationships
	Address *Address `gorm:"foreignKey:AddressID"`
	Wallet  *Wallet  `gorm:"foreignKey:WalletID"`
}

// TableName specifies the table name for Transaction model
func (t *Transaction) TableName() string {
	return "transactions"
}
