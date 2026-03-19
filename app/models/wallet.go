package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

// Wallet is an MPC co-signing wallet. The customer owns share_A (encrypted with
// their passphrase); the service holds share_B in AWS Secrets Manager.
// Neither party can sign alone.
type Wallet struct {
	orm.Model
	ID                 uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	Chain              string    `gorm:"type:varchar(50);not null;index" json:"chain"`
	Label              string    `gorm:"type:varchar(255)" json:"label,omitempty"`
	// MPC key material — never exposed in JSON responses (stored as hex-encoded text)
	MPCCustomerShare   string    `gorm:"type:text;not null" json:"-"`
	MPCShareIV         string    `gorm:"type:text;not null" json:"-"`
	MPCShareSalt       string    `gorm:"type:text;not null" json:"-"`
	MPCSecretARN       string    `gorm:"type:text;not null" json:"-"`
	MPCPublicKey       string    `gorm:"type:text;not null" json:"-"`
	MPCCurve           string    `gorm:"type:varchar(20);not null" json:"-"`
	DepositAddress     string    `gorm:"type:text;not null" json:"deposit_address"`
}

// TableName specifies the table name for Wallet model.
func (w *Wallet) TableName() string {
	return "wallets"
}
