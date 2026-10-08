package tron

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// tronGasSeedMarginPercent pads the TRX a child needs for its TRC-20 sweep over
	// the estimated cost (bandwidth plus the energy its sender pays). The transaction
	// still carries the full fee_limit, and a child holding less than the fee_limit
	// only lowers the call's energy ceiling to what it can pay, so the margin, like
	// tronEnergyMarginPercent, covers an energy estimate that grows by up to 20 %.
	tronGasSeedMarginPercent = int64(20)
	// tronFullUserResourcePercent: the caller pays all of the call's energy.
	tronFullUserResourcePercent = int64(100)

	tronSweepMetaEnergy   = "energy"
	tronSweepMetaFixedFee = "fixed_fee"
	tronSweepMetaRequired = "required_native"
)

// tronContractEnergyShare is how a contract splits a call's energy: the deployer
// (origin) pays (100 − consume_user_resource_percent) % of it, at most
// origin_energy_limit per call and only from the energy it has staked; the caller
// burns TRX for the rest.
type tronContractEnergyShare struct {
	userPercent       int64
	originEnergyLimit int64
	originAvailable   int64
}

// callerEnergy is the part of energy the caller pays.
func (s tronContractEnergyShare) callerEnergy(energy int64) int64 {
	if s.userPercent >= tronFullUserResourcePercent {
		return energy
	}
	originShare := energy * (tronFullUserResourcePercent - s.userPercent) / tronFullUserResourcePercent
	originShare = min(originShare, s.originEnergyLimit, max(s.originAvailable, 0))
	return energy - max(originShare, 0)
}

// contractEnergyShare reads the contract's energy split and its deployer's free
// energy (/wallet/getcontract, /wallet/getaccountresource).
func (a *TronLive) contractEnergyShare(ctx context.Context, contract string) (tronContractEnergyShare, error) {
	contractHex, err := addressing.TronAddressToHex(contract)
	if err != nil {
		return tronContractEnergyShare{}, err
	}
	var info struct {
		OriginAddress              string `json:"origin_address"`
		ConsumeUserResourcePercent int64  `json:"consume_user_resource_percent"`
		OriginEnergyLimit          int64  `json:"origin_energy_limit"`
	}
	if err := a.post(ctx, "/wallet/getcontract", map[string]any{"value": contractHex}, &info); err != nil {
		return tronContractEnergyShare{}, err
	}
	if info.OriginAddress == "" {
		return tronContractEnergyShare{}, fmt.Errorf("tron contract %s not found", contract)
	}
	share := tronContractEnergyShare{userPercent: info.ConsumeUserResourcePercent, originEnergyLimit: info.OriginEnergyLimit}
	if share.userPercent >= tronFullUserResourcePercent {
		return share, nil
	}
	var resource struct {
		EnergyLimit int64 `json:"EnergyLimit"`
		EnergyUsed  int64 `json:"EnergyUsed"`
	}
	if err := a.post(ctx, "/wallet/getaccountresource", map[string]any{"address": info.OriginAddress}, &resource); err != nil {
		return tronContractEnergyShare{}, err
	}
	share.originAvailable = resource.EnergyLimit - resource.EnergyUsed
	return share, nil
}

// requiredSweepFunding is the TRX a child must hold to send a TRC-20 sweep: its
// bandwidth (and any activation) plus the energy it pays itself, plus
// tronGasSeedMarginPercent, never more than maxFee — the transaction's own
// ceiling (bandwidth + activation + fee_limit). When the contract's energy split
// cannot be read, the caller is assumed to pay all of it.
func (a *TronLive) requiredSweepFunding(ctx context.Context, params tronChainParams, contract string, energy int64, fixedFee, maxFee *big.Int) *big.Int {
	callerEnergy := energy
	share, err := a.contractEnergyShare(ctx, contract)
	if err != nil {
		slog.Warn("tron contract energy share unavailable, funding the sweep for all of its energy", "contract", contract, "error", err)
	} else {
		callerEnergy = share.callerEnergy(energy)
	}
	cost := new(big.Int).Mul(big.NewInt(callerEnergy), big.NewInt(params.sunPerEnergy))
	if fixedFee != nil {
		cost.Add(cost, fixedFee)
	}
	required := ceilPercentOf(cost, percentDenominator+tronGasSeedMarginPercent)
	if maxFee != nil && required.Cmp(maxFee) > 0 {
		return new(big.Int).Set(maxFee)
	}
	return required
}

func ceilPercentOf(value *big.Int, percent int64) *big.Int {
	scaled := new(big.Int).Mul(value, big.NewInt(percent))
	scaled.Add(scaled, big.NewInt(percentDenominator-1))
	return scaled.Div(scaled, big.NewInt(percentDenominator))
}

// SweepFundingShortfall is the TRX the sender of a TRC-20 sweep built by BuildSweep
// still lacks, priced with the contract's current energy split: 0 when it holds
// enough, or for a TRX sweep, which pays its fee from the amount.
func (a *TronLive) SweepFundingShortfall(ctx context.Context, sweep *types.UnsignedTx) (*big.Int, error) {
	if sweep == nil {
		return nil, fmt.Errorf("tron sweep is required")
	}
	contract, _ := sweep.Metadata["token_contract"].(string)
	if contract == "" {
		return new(big.Int), nil
	}
	from, _ := sweep.Metadata["owner_address"].(string)
	energy, err := strconv.ParseInt(fmt.Sprint(sweep.Metadata[tronSweepMetaEnergy]), 10, 64)
	if err != nil || energy <= 0 {
		return nil, fmt.Errorf("tron sweep metadata has no energy estimate")
	}
	fixedFee, okFixed := new(big.Int).SetString(fmt.Sprint(sweep.Metadata[tronSweepMetaFixedFee]), 10)
	maxFee, okMax := new(big.Int).SetString(fmt.Sprint(sweep.Metadata["fee"]), 10)
	if !okFixed || !okMax {
		return nil, fmt.Errorf("tron sweep metadata has no fee")
	}
	params, err := a.chainParams(ctx)
	if err != nil {
		return nil, err
	}
	required := a.requiredSweepFunding(ctx, params, contract, energy, fixedFee, maxFee)
	balance, err := a.GetBalance(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("tron sweep sender %s balance: %w", from, err)
	}
	shortfall := new(big.Int).Sub(required, balance.Amount)
	if shortfall.Sign() < 0 {
		return new(big.Int), nil
	}
	return shortfall, nil
}
