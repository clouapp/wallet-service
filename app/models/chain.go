package models

import "github.com/goravel/framework/database/orm"

const (
	AdapterTypeEVM     = "evm"
	AdapterTypeBitcoin = "bitcoin"
	AdapterTypeSolana  = "solana"

	ChainETH     = "eth"
	ChainBTC     = "btc"
	ChainPolygon = "polygon"
	ChainSOL     = "sol"

	ChainTETH     = "teth"
	ChainTBTC     = "tbtc"
	ChainTPolygon = "tpolygon"
	ChainTSOL     = "tsol"

	ChainMatic = "matic"

	EnvironmentProd = "prod"
	EnvironmentTest = "test"
)

type Chain struct {
	orm.Model
	ID                    string  `gorm:"type:varchar(20);primary_key" json:"id"`
	Name                  string  `gorm:"type:varchar(100);not null" json:"name"`
	AdapterType           string  `gorm:"type:varchar(20);not null" json:"adapter_type"`
	NativeSymbol          string  `gorm:"type:varchar(20);not null" json:"native_symbol"`
	NativeDecimals        int     `gorm:"not null" json:"native_decimals"`
	NetworkID             *int64  `gorm:"type:bigint" json:"network_id,omitempty"`
	RpcURL                string  `gorm:"type:text;not null" json:"-"`
	IsTestnet             bool    `gorm:"default:false" json:"is_testnet"`
	MainnetChainID        *string `gorm:"type:varchar(20)" json:"mainnet_chain_id,omitempty"`
	RequiredConfirmations int     `gorm:"not null" json:"required_confirmations"`
	IconURL               *string `gorm:"type:varchar(500)" json:"icon_url,omitempty"`
	DisplayOrder          int     `gorm:"default:0" json:"display_order"`
	Status                string  `gorm:"type:varchar(20);default:active" json:"status"`
}

func (c *Chain) TableName() string { return "chains" }
