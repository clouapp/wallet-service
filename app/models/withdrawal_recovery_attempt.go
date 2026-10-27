package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	RecoveryClaimed             = "claimed"
	RecoveryGasClaimed          = "gas_claimed"
	RecoveryGasReady            = "gas_ready"
	RecoverySigning             = "signing"
	RecoverySigned              = "signed"
	RecoverySendClaimed         = "send_claimed"
	RecoverySendUnknown         = "send_unknown"
	RecoveryBroadcast           = "broadcast"
	RecoveryNeedsReconciliation = "needs_reconciliation"
)

// WithdrawalRecoveryAttempt reserves one business operation and sender nonce.
// The database makes its economic/review binding immutable and retains every
// state transition in withdrawal_recovery_events. Sealed bytes are never public.
type WithdrawalRecoveryAttempt struct {
	ID                     uuid.UUID `gorm:"type:uuid;primary_key"`
	OperationID            uuid.UUID `gorm:"type:uuid"`
	OriginalTransactionID  uuid.UUID `gorm:"type:uuid"`
	AccountID              uuid.UUID `gorm:"type:uuid"`
	WalletID               uuid.UUID `gorm:"type:uuid"`
	SourceAddressID        uuid.UUID `gorm:"type:uuid"`
	NetworkID              int64
	Sender                 string
	SenderNonce            string `gorm:"type:numeric(20,0)"`
	BindingHash            string
	BindingJSON            string `gorm:"type:jsonb"`
	ProposalHash           string
	ProposalJSON           string `gorm:"type:jsonb"`
	ReviewJSON             string `gorm:"type:jsonb"`
	Operator               string
	State                  string
	CallJSON               string     `gorm:"type:jsonb"`
	GasResultJSON          string     `gorm:"type:jsonb"`
	CandidateTransactionID *uuid.UUID `gorm:"type:uuid"`
	CandidateHash          string
	SealedSignedTx         string
	ObservationJSON        string `gorm:"type:jsonb"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (*WithdrawalRecoveryAttempt) TableName() string { return "withdrawal_recovery_attempts" }

// WithdrawalRecoveryUpdate has only execution observations, never economic fields.
type WithdrawalRecoveryUpdate struct {
	CallJSON        *string
	GasResultJSON   *string
	ObservationJSON *string
}

type WithdrawalRecoveryEvent struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key"`
	AttemptID       uuid.UUID `gorm:"type:uuid"`
	FromState       string
	ToState         string
	ObservationJSON string `gorm:"type:jsonb"`
	CreatedAt       time.Time
}

func (*WithdrawalRecoveryEvent) TableName() string { return "withdrawal_recovery_events" }
