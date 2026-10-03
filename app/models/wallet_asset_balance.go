package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletAssetBalance struct {
	orm.Model
	ID            uuid.UUID `gorm:"type:uuid;primary_key"`
	WalletID      uuid.UUID `gorm:"type:uuid;not null;index"`
	ChainID       string    `gorm:"type:varchar(32);not null;index"`
	AssetType     string    `gorm:"type:asset_type;not null"`
	AssetSymbol   string    `gorm:"type:varchar(32);not null"`
	AssetName     *string   `gorm:"type:varchar(128)"`
	AssetContract *string   `gorm:"type:varchar(255)"`
	AssetKey      string    `gorm:"type:varchar(320);not null"`
	Decimals      int       `gorm:"type:int;not null"`
	AmountRaw     string    `gorm:"type:text;not null"`
	AmountDisplay string    `gorm:"type:text;not null"`
	PriceUSD      *float64  `gorm:"type:numeric(28,10)"`
	ValueUSD      *float64  `gorm:"type:numeric(28,10)"`
	SourceAddress *string   `gorm:"type:text"`
	LastSyncedAt  time.Time `gorm:"type:timestamptz;not null"`

	Wallet *Wallet `gorm:"foreignKey:WalletID"`
}

func (w *WalletAssetBalance) TableName() string {
	return "wallet_asset_balances"
}
