package models

import (
	"math/big"

	"github.com/goravel/framework/database/orm"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/macrowallets/waas/pkg/numeric"
)

const (
	AdapterTypeEVM     = "evm"
	AdapterTypeBitcoin = "bitcoin"
	AdapterTypeSolana  = "solana"

	ChainETH      = "eth"
	ChainBTC      = "btc"
	ChainPolygon  = "polygon"
	ChainSOL      = "sol"
	ChainBase     = "base"
	ChainArbitrum = "arbitrum"
	ChainBSC      = "bsc"

	ChainTETH      = "teth"
	ChainTBTC      = "tbtc"
	ChainTPolygon  = "tpolygon"
	ChainTSOL      = "tsol"
	ChainTBase     = "tbase"
	ChainTArbitrum = "tarbitrum"
	ChainTBSC      = "tbsc"

	ChainMatic = "matic"

	NativeETH = "eth"
	NativePOL = "POL"
	NativeBTC = "btc"
	NativeSOL = "sol"
	NativeBNB = "bnb"

	SymbolUSDT = "USDT"
	SymbolUSDC = "USDC"

	USDTContractETH     = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
	USDCContractETH     = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	USDTContractPolygon = "0xc2132D05D31c914a87C6611C10748AEb04B58e8F"
	USDCContractPolygon = "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"
	USDTMintSOL         = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
	USDCMintSOL         = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

	// Stablecoins on Base, Arbitrum and BNB Smart Chain, checked on-chain with
	// symbol() and decimals(). BSC's Binance-Peg USDT/USDC use 18 decimals.
	USDCContractBase        = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	USDCContractBaseSepolia = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
	USDCContractArbitrum    = "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"
	USDTContractArbitrum    = "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"
	USDCContractArbSepolia  = "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d"
	USDTContractBSC         = "0x55d398326f99059fF775485246999027B3197955"
	USDCContractBSC         = "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d"

	EnvironmentProd = "prod"
	EnvironmentTest = "test"
)

type Chain struct {
	orm.Model
	ID                       string              `gorm:"type:varchar(20);primary_key"`
	Name                     string              `gorm:"type:varchar(100);not null"`
	AdapterType              string              `gorm:"type:varchar(20);not null"`
	NativeSymbol             string              `gorm:"type:varchar(20);not null"`
	NativeDecimals           int                 `gorm:"not null"`
	NetworkID                *int64              `gorm:"type:bigint"`
	RpcURL                   string              `gorm:"type:text;not null"`
	IsTestnet                bool                `gorm:"default:false"`
	MainnetChainID           *string             `gorm:"type:varchar(20)"`
	RequiredConfirmations    int                 `gorm:"not null"`
	IconURL                  *string             `gorm:"type:varchar(500)"`
	DisplayOrder             int                 `gorm:"default:0"`
	Status                   string              `gorm:"type:varchar(20);default:active"`
	GasReadinessThresholdRaw *string             `gorm:"type:text;not null"`
	DustThresholdNativeRaw   *string             `gorm:"type:text;not null"`
	DustThresholdUSD         numeric.NullDecimal `gorm:"type:decimal(16,4);not null"`
}

// ChainThresholdWrite is the subset of threshold columns a platform patch may
// store. A nil pointer leaves that column unchanged.
type ChainThresholdWrite struct {
	GasReadinessThresholdRaw *string
	DustThresholdNativeRaw   *string
	DustThresholdUSD         *string
}

func (c *Chain) TableName() string { return "chains" }

func (c *Chain) FlagScopeIdentifier() string {
	return "chain:" + c.ID
}

// BeforeCreate stores an empty raw threshold and a zero USD threshold when the
// caller left them unset. The columns are NOT NULL. An empty raw value is read
// as "not set", and zero USD disables token-dust filtering.
func (c *Chain) BeforeCreate(*gorm.DB) error {
	if c.GasReadinessThresholdRaw == nil {
		empty := ""
		c.GasReadinessThresholdRaw = &empty
	}
	if c.DustThresholdNativeRaw == nil {
		empty := ""
		c.DustThresholdNativeRaw = &empty
	}
	if !c.DustThresholdUSD.Valid {
		c.DustThresholdUSD = numeric.NewNullDecimal(decimal.Zero)
	}
	return nil
}

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
