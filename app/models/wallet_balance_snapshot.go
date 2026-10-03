package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletBalanceSnapshot struct {
	orm.Model
	ID             uuid.UUID `gorm:"type:uuid;primary_key"`
	WalletID       uuid.UUID `gorm:"type:uuid;not null;index"`
	ChainID        string    `gorm:"type:varchar(32);not null;index"`
	BalanceAsset   string    `gorm:"type:varchar(32);not null"`
	BalanceRaw     string    `gorm:"type:text;not null"`
	BalanceDisplay string    `gorm:"type:text;not null"`
	BalanceUSD     *float64  `gorm:"type:numeric(28,10)"`
	CapturedAt     time.Time `gorm:"type:timestamptz;not null"`

	Wallet *Wallet `gorm:"foreignKey:WalletID"`
}

func (w *WalletBalanceSnapshot) TableName() string {
	return "wallet_balance_snapshots"
}
