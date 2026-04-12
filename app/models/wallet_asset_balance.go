package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type WalletAssetBalance struct {
	orm.Model
	ID             uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
	WalletID       uuid.UUID `gorm:"type:uuid;not null;index" json:"wallet_id"`
	ChainID        string    `gorm:"type:varchar(32);not null;index" json:"chain_id"`
	AssetType      string    `gorm:"type:asset_type;not null" json:"asset_type"`
	AssetSymbol    string    `gorm:"type:varchar(32);not null" json:"asset_symbol"`
	AssetName      *string   `gorm:"type:varchar(128)" json:"asset_name,omitempty"`
	AssetContract  *string   `gorm:"type:varchar(255)" json:"asset_contract,omitempty"`
	AssetKey       string    `gorm:"type:varchar(320);not null" json:"asset_key"`
	Decimals       int       `gorm:"type:int;not null" json:"decimals"`
	AmountRaw      string    `gorm:"type:text;not null" json:"amount_raw"`
	AmountDisplay  string    `gorm:"type:text;not null" json:"amount_display"`
	PriceUSD       *float64  `gorm:"type:numeric(28,10)" json:"price_usd,omitempty"`
	ValueUSD       *float64  `gorm:"type:numeric(28,10)" json:"value_usd,omitempty"`
	SourceAddress  *string   `gorm:"type:text" json:"source_address,omitempty"`
	LastSyncedAt   time.Time `gorm:"type:timestamptz;not null" json:"last_synced_at"`

	Wallet *Wallet `gorm:"foreignKey:WalletID" json:"wallet,omitempty"`
}

func (w *WalletAssetBalance) TableName() string {
	return "wallet_asset_balances"
}
