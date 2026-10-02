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
	ID                  uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	AddressID           *uuid.UUID `gorm:"type:uuid;index" json:"address_id"`
	WalletID            uuid.UUID  `gorm:"type:uuid;not null;index" json:"wallet_id"`
	ExternalUserID      string     `gorm:"type:varchar(255);not null;index" json:"external_user_id"`
	Chain               string     `gorm:"type:varchar(50);not null;index" json:"chain"`
	TxType              string     `gorm:"type:transaction_type;not null;index" json:"tx_type"`
	TxHash              string     `gorm:"type:varchar(255);index" json:"tx_hash"`
	LogIndex            int        `gorm:"type:int;default:-1" json:"log_index"`
	FromAddress         string     `gorm:"type:varchar(255)" json:"from_address"`
	ToAddress           string     `gorm:"type:varchar(255);not null" json:"to_address"`
	Amount              string     `gorm:"type:varchar(100);not null" json:"amount"`
	Asset               string     `gorm:"type:varchar(50);not null" json:"asset"`
	TokenContract       string     `gorm:"type:varchar(255)" json:"token_contract"`
	Confirmations       int        `gorm:"type:int;not null;default:0" json:"confirmations"`
	RequiredConfs       int        `gorm:"type:int;not null;default:12" json:"required_confs"`
	Status              string     `gorm:"type:transaction_status;not null;index" json:"status"`
	Fee                 string     `gorm:"type:varchar(100)" json:"fee"`
	BlockNumber         int64      `gorm:"type:bigint;index:idx_chain_block" json:"block_number"`
	BlockHash           string     `gorm:"type:varchar(255)" json:"block_hash"`
	ErrorMessage        string     `gorm:"type:text" json:"error_message"`
	IdempotencyKey      *string    `gorm:"type:varchar(255)" json:"idempotency_key,omitempty"`
	ConfirmedAt         *time.Time `gorm:"type:timestamptz" json:"confirmed_at"`
	Direction           string     `gorm:"type:transaction_direction" json:"direction,omitempty"`
	Source              string     `gorm:"type:transaction_source" json:"source,omitempty"`
	ParentTransactionID *uuid.UUID `gorm:"type:uuid;index" json:"parent_transaction_id,omitempty"`
	Origin              string     `gorm:"type:varchar(24);index" json:"origin,omitempty"`
	RawPayload          string     `gorm:"type:jsonb" json:"-"`
	SyncedAt            *time.Time `gorm:"type:timestamptz" json:"synced_at,omitempty"`

	// Relationships
	Address *Address `gorm:"foreignKey:AddressID" json:"address,omitempty"`
	Wallet  *Wallet  `gorm:"foreignKey:WalletID" json:"wallet,omitempty"`
}

// TableName specifies the table name for Transaction model
func (t *Transaction) TableName() string {
	return "transactions"
}
