package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

// Wallet is an MPC co-signing wallet. The customer owns share_A (encrypted with
// their passphrase); the service holds share_B in AWS Secrets Manager.
// Neither party can sign alone.
type Wallet struct {
	orm.Model
	ID    uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	Chain string    `gorm:"type:varchar(50);not null;index" json:"chain"`
	Label string    `gorm:"type:varchar(255)" json:"label,omitempty"`
	// MPC key material — never exposed in JSON responses (stored as hex-encoded text)
	MPCCustomerShare string     `gorm:"type:text;not null" json:"-"`
	MPCShareIV       string     `gorm:"type:text;not null" json:"-"`
	MPCShareSalt     string     `gorm:"type:text;not null" json:"-"`
	MPCSecretARN     string     `gorm:"type:text;not null" json:"-"`
	MPCPublicKey     string     `gorm:"type:text;not null" json:"-"`
	MPCCurve         string     `gorm:"type:varchar(20);not null" json:"-"`
	DepositAddressID *uuid.UUID `gorm:"type:uuid" json:"deposit_address_id,omitempty"`
	// Account and admin fields
	AccountID         *uuid.UUID `gorm:"type:uuid;index" json:"account_id,omitempty"`
	Status            string     `gorm:"type:wallet_status;default:active" json:"status"`
	FeeRateMin        *int       `gorm:"type:integer" json:"fee_rate_min,omitempty"`
	FeeRateMax        *int       `gorm:"type:integer" json:"fee_rate_max,omitempty"`
	FeeMultiplier     *float64   `gorm:"type:decimal(8,4)" json:"fee_multiplier,omitempty"`
	RequiredApprovals int        `gorm:"default:1" json:"required_approvals"`
	FrozenUntil       *time.Time `json:"frozen_until,omitempty"`
	ActivationCode    *string    `gorm:"type:char(6)" json:"-"`

	BalanceAsset        *string    `gorm:"type:varchar(32)" json:"balance_asset,omitempty"`
	BalanceRaw          *string    `gorm:"type:text" json:"balance_raw,omitempty"`
	BalanceDisplay      *string    `gorm:"type:text" json:"balance,omitempty"`
	BalanceUSD          *float64   `gorm:"type:decimal(28,10)" json:"balance_usd,omitempty"`
	BalanceLastSyncedAt *time.Time `gorm:"type:timestamptz" json:"balance_last_synced_at,omitempty"`
	ReadModelStatus     string     `gorm:"type:wallet_read_model_status;default:idle" json:"read_model_status"`

	DepositAddress *Address `gorm:"foreignKey:DepositAddressID" json:"deposit_address,omitempty"`
}

// TableName specifies the table name for Wallet model.
func (w *Wallet) TableName() string {
	return "wallets"
}
