package models

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/pkg/mpcshare"
	"github.com/macrowallets/waas/pkg/numeric"
)

const (
	GasStatusUnseeded = "unseeded"
	GasStatusSeeded   = "seeded"
	GasStatusLow      = "low"
)

// WalletStatusArchived marks a retired wallet (wallet_status enum).
const WalletStatusArchived = "archived"

// Wallet is an MPC co-signing wallet. The customer owns share_A (encrypted with
// their passphrase); the service holds share_B in AWS Secrets Manager.
// Neither party can sign alone.
type Wallet struct {
	orm.Model
	ID    uuid.UUID `gorm:"type:uuid;primary_key"`
	Chain string    `gorm:"type:varchar(50);not null;index"`
	Label string    `gorm:"type:varchar(255)"`
	// MPC key material — never part of an HTTP body (stored as hex-encoded text)
	MPCCustomerShare string     `gorm:"type:text;not null"`
	MPCShareIV       string     `gorm:"type:text;not null"`
	MPCShareSalt     string     `gorm:"type:text;not null"`
	MPCSecretARN     string     `gorm:"type:text;not null"`
	MPCPublicKey     string     `gorm:"type:text;not null"`
	MPCCurve         string     `gorm:"type:varchar(20);not null"`
	MPCChainCode     string     `gorm:"type:text"`
	AddressIndex     int        `gorm:"type:integer;not null;default:0"`
	DepositAddressID *uuid.UUID `gorm:"type:uuid"`
	// Account and admin fields
	AccountID         *uuid.UUID          `gorm:"type:uuid;index"`
	Status            string              `gorm:"type:wallet_status;default:active"`
	FeeRateMin        *int                `gorm:"type:integer"`
	FeeRateMax        *int                `gorm:"type:integer"`
	FeeMultiplier     numeric.NullDecimal `gorm:"type:decimal(8,4)"`
	RequiredApprovals int                 `gorm:"default:1"`
	FrozenUntil       *time.Time
	ActivationCode    *string             `gorm:"type:char(6)"`

	BalanceAsset        *string             `gorm:"type:varchar(32)"`
	BalanceRaw          *string             `gorm:"type:text"`
	BalanceDisplay      *string             `gorm:"type:text"`
	BalanceUSD          numeric.NullDecimal `gorm:"type:decimal(28,10)"`
	BalanceLastSyncedAt *time.Time          `gorm:"type:timestamptz"`
	ReadModelStatus     string              `gorm:"type:wallet_read_model_status;default:idle"`
	GasStatus          string     `gorm:"type:wallet_gas_status;not null;default:unseeded;index"`
	GasLastCheckedAt   *time.Time `gorm:"type:timestamptz"`
	SweepPolicyVersion int        `gorm:"not null;default:1"`

	DepositAddress *Address `gorm:"foreignKey:DepositAddressID"`
}

// TableName specifies the table name for Wallet model.
func (w *Wallet) TableName() string {
	return "wallets"
}

// DecryptShareA hex-decodes the wallet's persisted MPC envelope
// (ciphertext / IV / salt) and reverses the AES-GCM encryption using the
// caller-supplied passphrase. On AES auth failure it returns
// mpcshare.ErrInvalidPassphrase so callers can branch on rate-limiting /
// HTTP mapping; structural errors (bad hex, crypto init) are wrapped.
// Callers own the returned slice and MUST zero it after use.
func (w *Wallet) DecryptShareA(passphrase string) ([]byte, error) {
	ciphertext, err := hex.DecodeString(w.MPCCustomerShare)
	if err != nil {
		return nil, fmt.Errorf("decode customer share: %w", err)
	}
	iv, err := hex.DecodeString(w.MPCShareIV)
	if err != nil {
		return nil, fmt.Errorf("decode share iv: %w", err)
	}
	salt, err := hex.DecodeString(w.MPCShareSalt)
	if err != nil {
		return nil, fmt.Errorf("decode share salt: %w", err)
	}
	enc := &mpcshare.EncryptedShare{Ciphertext: ciphertext, IV: iv, Salt: salt}
	return mpcshare.DecryptShare(enc, passphrase)
}
