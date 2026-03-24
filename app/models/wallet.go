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
	ID               uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	Chain            string     `gorm:"type:varchar(50);not null;index" json:"chain"`
	Label            string     `gorm:"type:varchar(255)" json:"label,omitempty"`
	// MPC key material — never exposed in JSON responses (stored as hex-encoded text)
	MPCCustomerShare string     `gorm:"type:text;not null" json:"-"`
	MPCShareIV       string     `gorm:"type:text;not null" json:"-"`
	MPCShareSalt     string     `gorm:"type:text;not null" json:"-"`
	MPCSecretARN     string     `gorm:"type:text;not null" json:"-"`
	MPCPublicKey     string     `gorm:"type:text;not null" json:"-"`
	MPCCurve         string     `gorm:"type:varchar(20);not null" json:"-"`
	DepositAddress   string     `gorm:"type:text;not null" json:"deposit_address"`
	// Account and admin fields
	AccountID         *uuid.UUID `gorm:"type:uuid;index" json:"account_id,omitempty"`
	Status            string     `gorm:"type:varchar(20);default:active" json:"status"`
	FeeRateMin        *int       `gorm:"type:integer" json:"fee_rate_min,omitempty"`
	FeeRateMax        *int       `gorm:"type:integer" json:"fee_rate_max,omitempty"`
	FeeMultiplier     *float64   `gorm:"type:decimal(8,4)" json:"fee_multiplier,omitempty"`
	RequiredApprovals int        `gorm:"default:1" json:"required_approvals"`
	FrozenUntil       *time.Time `json:"frozen_until,omitempty"`
	ActivationCode    *string    `gorm:"type:char(6)" json:"-"`
}

// TableName specifies the table name for Wallet model.
func (w *Wallet) TableName() string {
	return "wallets"
}
