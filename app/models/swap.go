package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

// SwapQuote stores the public parameters and the private provider route accepted by a caller.
type SwapQuote struct {
	orm.Model
	ID                  uuid.UUID `gorm:"type:uuid;primary_key"`
	AccountID           uuid.UUID `gorm:"type:uuid;not null;index"`
	SourceWalletID      uuid.UUID `gorm:"type:uuid;not null;index"`
	DestinationWalletID uuid.UUID `gorm:"type:uuid;not null"`
	Provider            string    `gorm:"type:varchar(16);not null"`
	RouteID             string    `gorm:"type:text;not null"`
	RequestPayload      string    `gorm:"type:text;not null"`
	ExpectedRaw         string    `gorm:"type:text;not null"`
	MinimumRaw          string    `gorm:"type:text;not null"`
	FeesPayload         string    `gorm:"type:text;not null"`
	AttemptsPayload     string    `gorm:"type:text;not null"`
	RoutePayload        string    `gorm:"type:text;not null"`
	EstimatedSeconds    int
	ExpiresAt           time.Time `gorm:"type:timestamptz;not null"`
}

// TableName identifies persisted executable quotes.
func (*SwapQuote) TableName() string { return "swap_quotes" }

// SwapOrder pins one account's idempotent execution to one immutable quote.
type SwapOrder struct {
	orm.Model
	ID                  uuid.UUID  `gorm:"type:uuid;primary_key"`
	AccountID           uuid.UUID  `gorm:"type:uuid;not null;index"`
	QuoteID             uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex"`
	IdempotencyKey      uuid.UUID  `gorm:"type:uuid;not null"`
	SourceWalletID      uuid.UUID  `gorm:"type:uuid;not null;index"`
	DestinationWalletID uuid.UUID  `gorm:"type:uuid;not null"`
	Status              string     `gorm:"type:varchar(24);not null"`
	Provider            string     `gorm:"type:varchar(16);not null"`
	FundingPayload      string     `gorm:"type:text;not null;default:'{}'"`
	SourceTxHash        string     `gorm:"type:text;not null;default:''"`
	DestinationTxHash   string     `gorm:"type:text;not null;default:''"`
	ReceivedRaw         string     `gorm:"type:text;not null;default:'0'"`
	RefundTxHash        string     `gorm:"type:text;not null;default:''"`
	RefundRaw           string     `gorm:"type:text;not null;default:'0'"`
	FailureCode         string     `gorm:"type:varchar(64);not null;default:''"`
	LastCheckedAt       *time.Time `gorm:"type:timestamptz"`
	AuthorizedUserID    *uuid.UUID `gorm:"type:uuid"`
	AuthorizedTokenID   *uuid.UUID `gorm:"type:uuid"`
	AuthorizedUntil     time.Time  `gorm:"type:timestamptz;not null"`
}

// TableName identifies account-scoped swap orders.
func (*SwapOrder) TableName() string { return "swap_orders" }

// SwapExecutionStep identifies signed bytes durably recorded before any send.
// The linked transaction holds the encrypted signed payload for reconciliation.
type SwapExecutionStep struct {
	orm.Model
	ID            uuid.UUID `gorm:"type:uuid;primary_key"`
	OrderID       uuid.UUID `gorm:"type:uuid;not null;index"`
	TransactionID uuid.UUID `gorm:"type:uuid;not null"`
	Position      int
	Purpose       string `gorm:"type:varchar(24);not null"`
	TxHash        string `gorm:"type:text;not null"`
}

func (*SwapExecutionStep) TableName() string { return "swap_execution_steps" }

// SwapCreditAllocation prevents reusing the same observed funds across orders,
// including providers that batch multiple payouts to one custody address.
type SwapCreditAllocation struct {
	orm.Model
	ID        uuid.UUID `gorm:"type:uuid;primary_key"`
	OrderID   uuid.UUID `gorm:"type:uuid;not null"`
	WalletID  uuid.UUID `gorm:"type:uuid;not null"`
	Direction string    `gorm:"type:varchar(8);not null"`
	Chain     string    `gorm:"type:varchar(50);not null"`
	AssetKey  string    `gorm:"type:text;not null"`
	Address   string    `gorm:"type:text;not null"`
	TxHash    string    `gorm:"type:text;not null"`
	AmountRaw string    `gorm:"type:text;not null"`
}

func (*SwapCreditAllocation) TableName() string { return "swap_credit_allocations" }
