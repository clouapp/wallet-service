package feeestimate

import (
	"math/big"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/pkg/amount"
)

// Estimate is the API answer. Amounts are non-negative integers in base units
// (*_base_units) and their decimal renderings; the fee is always in FeeAsset,
// the chain's native coin.
type Estimate struct {
	WalletID                  string    `json:"wallet_id" example:"d6a8ce92-b637-44c2-a9f1-774344802e1a"`
	Chain                     string    `json:"chain" example:"eth"`
	Asset                     string    `json:"asset" example:"ETH"`
	Amount                    string    `json:"amount" example:"0.001"`
	AmountBaseUnits           string    `json:"amount_base_units" example:"1000000000000000"`
	AmountIsReference         bool      `json:"amount_is_reference" example:"false"`
	FeeAsset                  string    `json:"fee_asset" example:"ETH"`
	FeeDecimals               int       `json:"fee_decimals" example:"18"`
	Fee                       string    `json:"fee" example:"0.000041893278336"`
	FeeBaseUnits              string    `json:"fee_base_units" example:"41893278336000"`
	FeeMultiplier             string    `json:"fee_multiplier" example:"1"`
	Strategy                  string    `json:"strategy" example:"direct_from_base"`
	Basis                     string    `json:"basis" example:"plan"`
	Transfers                 int       `json:"transfers" example:"1"`
	Recipient                 string    `json:"recipient" example:"provided"`
	AmountSpendable           bool      `json:"amount_spendable" example:"true"`
	InsufficientFunds         bool      `json:"insufficient_funds" example:"false"`
	BaseBalanceBaseUnits      string    `json:"base_balance_base_units" example:"1958106721664000"`
	MinimumRemainingBaseUnits string    `json:"minimum_remaining_base_units" example:"0"`
	Details                   Details   `json:"details"`
	EstimatedAt               time.Time `json:"estimated_at"`
	ExpiresAt                 time.Time `json:"expires_at"`
	Cached                    bool      `json:"cached" example:"false"`
}

// Details holds the chain-specific inputs of the fee; exactly one is set.
type Details struct {
	EVM     *EVMDetails     `json:"evm,omitempty"`
	Bitcoin *BitcoinDetails `json:"bitcoin,omitempty"`
	Solana  *SolanaDetails  `json:"solana,omitempty"`
	Tron    *TronDetails    `json:"tron,omitempty"`
}

// EVMDetails: legacy transactions pay GasPriceWei for every unit of gas used, so
// fee = gas_limit × gas_price_wei + l1_data_fee_wei is also the most they can cost.
type EVMDetails struct {
	TxType       string `json:"tx_type" example:"legacy"`
	GasLimit     uint64 `json:"gas_limit" example:"21000"`
	GasPriceWei  string `json:"gas_price_wei" example:"1994918016"`
	GasPriceGwei string `json:"gas_price_gwei" example:"1.994918016"`
	L1DataFeeWei string `json:"l1_data_fee_wei" example:"0"`
}

// BitcoinDetails: fee = ceil(fee_rate × vsize), never below 1 sat/vB.
type BitcoinDetails struct {
	FeeRateSatPerVByte string `json:"fee_rate_sat_per_vbyte" example:"1"`
	FeeRateSource      string `json:"fee_rate_source" example:"estimator"`
	VSize              int64  `json:"vsize" example:"141"`
	Inputs             int    `json:"inputs" example:"1"`
	Outputs            int    `json:"outputs" example:"2"`
}

// SolanaDetails: fee = signatures × lamports_per_signature + token_account_creation_lamports.
type SolanaDetails struct {
	Signatures                   int    `json:"signatures" example:"1"`
	LamportsPerSignature         int64  `json:"lamports_per_signature" example:"5000"`
	TokenAccountCreationLamports string `json:"token_account_creation_lamports" example:"0"`
}

// TronDetails: fee = bandwidth_fee_sun + account_activation_fee_sun + energy_fee_limit_sun,
// all burned TRX with no staked or free resources assumed. A TRX transfer to a new
// account pays the activation instead of bandwidth; energy_fee_limit_sun is the
// fee_limit of the TRC-20 calls, the most their energy can burn.
type TronDetails struct {
	BandwidthBytes          int64  `json:"bandwidth_bytes" example:"345"`
	SunPerBandwidthByte     int64  `json:"sun_per_bandwidth_byte" example:"1000"`
	BandwidthFeeSun         string `json:"bandwidth_fee_sun" example:"345000"`
	AccountActivationFeeSun string `json:"account_activation_fee_sun" example:"0"`
	Energy                  int64  `json:"energy" example:"21975"`
	SunPerEnergy            int64  `json:"sun_per_energy" example:"100"`
	EnergyFeeLimitSun       string `json:"energy_fee_limit_sun" example:"2637000"`
	EnergyIsReference       bool   `json:"energy_is_reference" example:"false"`
}

func buildEstimate(req *resolvedRequest, quote *sweep.FeeQuote, estimatedAt, expiresAt time.Time) *Estimate {
	recipient := recipientProvided
	if quote.RecipientIsProbe {
		recipient = recipientProbe
	}
	return &Estimate{
		WalletID:                  req.wallet.ID.String(),
		Chain:                     quote.Chain,
		Asset:                     strings.ToUpper(quote.Asset),
		Amount:                    amount.FormatBaseUnits(req.baseUnits, req.asset.Decimals),
		AmountBaseUnits:           req.baseUnits.String(),
		AmountIsReference:         req.isReference,
		FeeAsset:                  strings.ToUpper(quote.FeeAsset),
		FeeDecimals:               req.chain.NativeDecimals,
		Fee:                       amount.FormatBaseUnits(quote.Fee, req.chain.NativeDecimals),
		FeeBaseUnits:              quote.Fee.String(),
		FeeMultiplier:             feeMultiplierString(quote),
		Strategy:                  string(quote.Strategy),
		Basis:                     string(quote.Basis),
		Transfers:                 quote.Transfers,
		Recipient:                 recipient,
		AmountSpendable:           quote.AmountSpendable,
		InsufficientFunds:         !quote.AmountSpendable,
		BaseBalanceBaseUnits:      nonNegativeString(quote.BaseBalance),
		MinimumRemainingBaseUnits: nonNegativeString(quote.MinimumRemaining),
		Details:                   buildDetails(quote),
		EstimatedAt:               estimatedAt,
		ExpiresAt:                 expiresAt,
	}
}

// feeMultiplierString renders the wallet fee multiplier the quote applied ("1" when none).
func feeMultiplierString(quote *sweep.FeeQuote) string {
	if quote.FeeMultiplier.IsZero() {
		return models.FeeMultiplierMin.String()
	}
	return quote.FeeMultiplier.String()
}

func buildDetails(quote *sweep.FeeQuote) Details {
	var details Details
	if quote.EVM != nil {
		details.EVM = &EVMDetails{
			TxType:       evmTxTypeLegacy,
			GasLimit:     quote.EVM.GasLimit,
			GasPriceWei:  nonNegativeString(quote.EVM.GasPrice),
			GasPriceGwei: amount.FormatBaseUnits(quote.EVM.GasPrice, gweiDecimals),
			L1DataFeeWei: nonNegativeString(quote.EVM.L1DataFee),
		}
	}
	if quote.Bitcoin != nil {
		source := feeRateSourceEstimator
		if quote.Bitcoin.FlatFallback {
			source = feeRateSourceFlat
		}
		details.Bitcoin = &BitcoinDetails{
			FeeRateSatPerVByte: amount.FormatBaseUnits(big.NewInt(quote.Bitcoin.MilliSatPerVByte), milliSatDecimals),
			FeeRateSource:      source,
			VSize:              quote.Bitcoin.VSize,
			Inputs:             quote.Bitcoin.Inputs,
			Outputs:            quote.Bitcoin.Outputs,
		}
	}
	if quote.Solana != nil {
		details.Solana = &SolanaDetails{
			Signatures:                   quote.Solana.Signatures,
			LamportsPerSignature:         quote.Solana.LamportsPerSignature,
			TokenAccountCreationLamports: nonNegativeString(quote.Solana.AccountCreationLamports),
		}
	}
	if quote.Tron != nil {
		details.Tron = &TronDetails{
			BandwidthBytes:          quote.Tron.BandwidthBytes,
			SunPerBandwidthByte:     quote.Tron.SunPerBandwidthByte,
			BandwidthFeeSun:         nonNegativeString(quote.Tron.BandwidthFee),
			AccountActivationFeeSun: nonNegativeString(quote.Tron.ActivationFee),
			Energy:                  quote.Tron.Energy,
			SunPerEnergy:            quote.Tron.SunPerEnergy,
			EnergyFeeLimitSun:       nonNegativeString(quote.Tron.EnergyFeeLimit),
			EnergyIsReference:       quote.Tron.EnergyIsReference,
		}
	}
	return details
}

// nonNegativeString renders a base-unit amount, never negative (nil and negatives are "0").
func nonNegativeString(value *big.Int) string {
	if value == nil || value.Sign() < 0 {
		return "0"
	}
	return value.String()
}
