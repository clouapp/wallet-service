package models

import (
	"math/big"

	"github.com/goravel/framework/database/orm"
)

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

	NativeETH = "eth"
	NativePOL = "POL"
	NativeBTC = "btc"
	NativeSOL = "sol"

	SymbolUSDT = "USDT"
	SymbolUSDC = "USDC"

	USDTContractETH     = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
	USDCContractETH     = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	USDTContractPolygon = "0xc2132D05D31c914a87C6611C10748AEb04B58e8F"
	USDCContractPolygon = "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"
	USDTMintSOL         = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
	USDCMintSOL         = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

	EnvironmentProd = "prod"
	EnvironmentTest = "test"
)

type Chain struct {
	orm.Model
	ID                       string   `gorm:"type:varchar(20);primary_key"`
	Name                     string   `gorm:"type:varchar(100);not null"`
	AdapterType              string   `gorm:"type:varchar(20);not null"`
	NativeSymbol             string   `gorm:"type:varchar(20);not null"`
	NativeDecimals           int      `gorm:"not null"`
	NetworkID                *int64   `gorm:"type:bigint"`
	RpcURL                   string   `gorm:"type:text;not null"`
	IsTestnet                bool     `gorm:"default:false"`
	MainnetChainID           *string  `gorm:"type:varchar(20)"`
	RequiredConfirmations    int      `gorm:"not null"`
	IconURL                  *string  `gorm:"type:varchar(500)"`
	DisplayOrder             int      `gorm:"default:0"`
	Status                   string   `gorm:"type:varchar(20);default:active"`
	GasReadinessThresholdRaw *string  `gorm:"type:text"`
	DustThresholdNativeRaw   *string  `gorm:"type:text"`
	DustThresholdUSD         *float64 `gorm:"type:decimal(16,4)"`
}

func (c *Chain) TableName() string { return "chains" }

func (c *Chain) GasReadinessThreshold() *big.Int {
	if c.GasReadinessThresholdRaw == nil || *c.GasReadinessThresholdRaw == "" {
		return nil
	}
	v, ok := new(big.Int).SetString(*c.GasReadinessThresholdRaw, 10)
	if !ok {
		return nil
	}
	return v
}

func (c *Chain) DustThresholdNative() *big.Int {
	if c.DustThresholdNativeRaw == nil || *c.DustThresholdNativeRaw == "" {
		return nil
	}
	v, ok := new(big.Int).SetString(*c.DustThresholdNativeRaw, 10)
	if !ok {
		return nil
	}
	return v
}
