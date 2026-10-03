package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"

	"github.com/macrowallets/waas/pkg/numeric"
)

type WalletBalanceSnapshot struct {
	orm.Model
	ID             uuid.UUID           `gorm:"type:uuid;primary_key" json:"id"`
	WalletID       uuid.UUID           `gorm:"type:uuid;not null;index" json:"wallet_id"`
	ChainID        string              `gorm:"type:varchar(32);not null;index" json:"chain_id"`
	BalanceAsset   string              `gorm:"type:varchar(32);not null" json:"balance_asset"`
	BalanceRaw     string              `gorm:"type:text;not null" json:"balance_raw"`
	BalanceDisplay string              `gorm:"type:text;not null" json:"balance_display"`
	BalanceUSD     numeric.NullDecimal `gorm:"type:numeric(28,10)" json:"balance_usd,omitzero" swaggertype:"number" example:"1250.5"`
	CapturedAt     time.Time           `gorm:"type:timestamptz;not null" json:"captured_at"`

	Wallet *Wallet `gorm:"foreignKey:WalletID" json:"wallet,omitempty"`
}

func (w *WalletBalanceSnapshot) TableName() string {
	return "wallet_balance_snapshots"
}
