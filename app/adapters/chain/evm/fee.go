package evm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/macrowallets/waas/app/services/chain"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// opStackGasPriceOracle is the OP-stack predeploy that quotes the L1 data fee.
	opStackGasPriceOracle = "0x420000000000000000000000000000000000000F"
	// opStackGetL1FeeSelector is getL1Fee(bytes): the fee for posting an unsigned
	// RLP-encoded transaction to L1 (the oracle adds the signature overhead).
	opStackGetL1FeeSelector = "49948e0e"
	// evmL1DataFeeMultiplier pads the quoted L1 data fee: the L1 base and blob fees
	// can rise between the quote and inclusion.
	evmL1DataFeeMultiplier = int64(2)
	// evmNativeGasMarginPercent pads the node's estimate of a native transfer on
	// every network but Arbitrum. A plain transfer's gas depends on whether its
	// recipient exists, not on the amount: on Sepolia (2026-10-18) creating the
	// recipient's account took 207,391 gas against 21,000 for an existing one, and
	// the estimate already prices that. The 20 % covers what can change between the
	// estimate and inclusion: a recipient whose code runs on receipt (a contract
	// wallet, an EIP-7702 delegation) reading state that moves, and estimators that
	// stop their binary search a little short. Unused gas is not charged; the
	// margin only raises what the sender must hold (limit × price).
	evmNativeGasMarginPercent = uint64(120)
	// evmArbitrumNativeGasMarginPercent pads Arbitrum's native transfer estimate,
	// whose L1 component follows the L1 price.
	evmArbitrumNativeGasMarginPercent = uint64(150)
	// evmFeeProbeGasLimit is the gas limit of the transaction the L1 data fee is
	// sized with when the real limit is not known yet: as wide as an estimated one.
	evmFeeProbeGasLimit = uint64(1<<24 - 1)
	abiWordBytes        = 32
	evmAddressBytes     = 20
)

// Representative transfer used to size fees before the real transfer is known.
// Its fields are as wide as a real transfer's so the L1 data fee is not undersized.
var (
	evmFeeProbeAddress = common.HexToAddress(chain.EVMFeeProbeRecipient())
	evmFeeProbeNonce   = uint64(1<<32 - 1)
	evmFeeProbeAmount  = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
)

// evmNativeSizingValue is the value a native transfer is estimated with when its
// amount is not known yet (a gas_seed the planner sizes, a sweep sized before its
// fee): a positive value is what makes a transfer create its recipient's account,
// and its size does not change a plain transfer's gas.
var evmNativeSizingValue = big.NewInt(1)

func (a *EVMLive) feeModel() models.EVMFeeModel {
	return models.EVMFeeModelOf(a.cfg.NetworkID)
}

func (a *EVMLive) nativeGasMarginPercent() uint64 {
	if a.feeModel() == models.EVMFeeModelArbitrum {
		return evmArbitrumNativeGasMarginPercent
	}
	return evmNativeGasMarginPercent
}

// NativeTransferGasLimit is the gas limit of a native transfer from → to whose
// amount is not known yet (a gas_seed the sweep planner sizes, a native sweep):
// nativeTransferGasLimit with evmNativeSizingValue. An empty from is the node's
// default sender; an empty to is a recipient that does not exist yet.
func (a *EVMLive) NativeTransferGasLimit(ctx context.Context, from, to string) (uint64, error) {
	return a.nativeTransferGasLimit(ctx, from, to, nil)
}

// nativeTransferGasLimit is the gas limit BuildTransfer encodes for a native
// transfer of value from → to: the node's eth_estimateGas of that transfer,
// padded by nativeGasMarginPercent, on every EVM network.
//
// There is no fixed limit to fall back to. Since Sepolia priced state creation,
// a transfer that creates its recipient's account needs ten times the old fixed
// 21000, and one sent with 21000 is mined out of gas: its fee is paid and its
// value is not moved. A failed estimate therefore fails the transfer before
// anything is signed: chain.ErrGasEstimateFailed, wrapping the node's error, so
// an outage still matches chain.ErrProviderUnavailable / chain.ErrRateLimited
// (chain.KnownFailure) and the caller may retry it.
//
// A nil or non-positive value is sized with evmNativeSizingValue. An empty from
// is the node's default sender. An empty to, or the shared fee probe recipient,
// stands for a recipient not known yet and is sized as a fresh address: the most
// a transfer to an externally owned account can need.
//
// A node refuses a transfer whose sender does not hold its value yet: the base
// address of a multi_sweep before its sweeps land, or a quote for a wallet not
// funded yet. When the node refuses (not an outage) and from holds less than
// value, the transfer is estimated again from the node's default sender with
// evmNativeSizingValue: a plain transfer's gas depends neither on who sends it nor
// on how much. A sender that holds value and is still refused (the transfer
// reverts) fails the estimate.
func (a *EVMLive) nativeTransferGasLimit(ctx context.Context, from, to string, value *big.Int) (uint64, error) {
	if value == nil || value.Sign() <= 0 {
		value = evmNativeSizingValue
	}
	recipient, err := nativeEstimateRecipient(to)
	if err != nil {
		return 0, fmt.Errorf("%w: native transfer on %s: %w", chain.ErrGasEstimateFailed, a.cfg.ChainIDStr, err)
	}
	estimate, err := a.estimateNativeGas(ctx, from, recipient, value)
	if err != nil && from != "" && !transientRPCFailure(ctx, err) && a.holdsLessThan(ctx, from, value) {
		estimate, err = a.estimateNativeGas(ctx, "", recipient, evmNativeSizingValue)
	}
	if err != nil {
		return 0, fmt.Errorf("%w: native transfer of %s from %s to %s on %s: %w",
			chain.ErrGasEstimateFailed, value, senderLabel(from), recipient, a.cfg.ChainIDStr, err)
	}
	if estimate == 0 {
		return 0, fmt.Errorf("%w: node returned no gas for a native transfer to %s on %s", chain.ErrGasEstimateFailed, recipient, a.cfg.ChainIDStr)
	}
	return estimate * a.nativeGasMarginPercent() / percentDenominator, nil
}

func (a *EVMLive) estimateNativeGas(ctx context.Context, from, to string, value *big.Int) (uint64, error) {
	call := map[string]string{"to": to, "value": "0x" + value.Text(16)}
	if from != "" {
		call["from"] = from
	}
	var hexEstimate string
	if err := a.rpc.Call(ctx, "eth_estimateGas", &hexEstimate, call); err != nil {
		return 0, err
	}
	return hexToUint64(hexEstimate), nil
}

// holdsLessThan reports whether address is known to hold less than value. A
// balance that cannot be read is not known to be short.
func (a *EVMLive) holdsLessThan(ctx context.Context, address string, value *big.Int) bool {
	balance, err := a.GetBalance(ctx, address)
	if err != nil || balance == nil || balance.Amount == nil {
		return false
	}
	return balance.Amount.Cmp(value) < 0
}

// transientRPCFailure is a call the node did not answer (timeout, outage, rate
// limit, cancelled context): it said nothing about the transfer.
func transientRPCFailure(ctx context.Context, err error) bool {
	return ctx.Err() != nil || chain.KnownFailure(err) ||
		errors.Is(err, chain.ErrProviderUnavailable) || errors.Is(err, chain.ErrRateLimited)
}

// nativeEstimateRecipient is the recipient a native transfer is estimated to: to,
// or a fresh random address when to is empty or the shared fee probe recipient,
// which anyone can fund and so may exist on chain.
func nativeEstimateRecipient(to string) (string, error) {
	if to != "" && !strings.EqualFold(to, evmFeeProbeAddress.Hex()) {
		return to, nil
	}
	random := make([]byte, evmAddressBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("draw a fresh recipient: %w", err)
	}
	return common.BytesToAddress(random).Hex(), nil
}

func senderLabel(from string) string {
	if from == "" {
		return "the node's default sender"
	}
	return from
}

// EstimateL1DataFee is what the network charges req's sender on top of gas limit ×
// gas price: on OP-stack networks the buffered L1 data fee of the transaction
// BuildTransfer would encode, zero elsewhere (no RPC call).
func (a *EVMLive) EstimateL1DataFee(ctx context.Context, req types.TransferRequest) (*big.Int, error) {
	if a.feeModel() != models.EVMFeeModelOPStack {
		return new(big.Int), nil
	}
	gasPrice := req.GasPrice
	if gasPrice == nil {
		suggested, err := a.EstimateGasPrice(ctx)
		if err != nil {
			return nil, err
		}
		gasPrice = suggested
	}
	gasLimit := uint64(evmERC20TransferGasFloor)
	if req.Token == nil {
		gasLimit = evmFeeProbeGasLimit
	}
	if req.GasLimit != nil && *req.GasLimit > 0 {
		gasLimit = *req.GasLimit
	}
	return a.l1DataFee(ctx, feeProbeTransaction(req, gasLimit, gasPrice))
}

// transferFee is the most a transfer encoded with gasLimit at gasPrice can cost
// its sender: gas limit × gas price plus the network's L1 data fee.
func (a *EVMLive) transferFee(ctx context.Context, req types.TransferRequest, gasLimit uint64, gasPrice *big.Int) (*big.Int, error) {
	fee := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
	if a.feeModel() != models.EVMFeeModelOPStack {
		return fee, nil
	}
	l1Fee, err := a.l1DataFee(ctx, feeProbeTransaction(req, gasLimit, gasPrice))
	if err != nil {
		return nil, err
	}
	return fee.Add(fee, l1Fee), nil
}

func (a *EVMLive) l1DataFee(ctx context.Context, transaction *gethtypes.Transaction) (*big.Int, error) {
	unsignedRLP, err := transaction.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("encode transaction for the L1 data fee: %w", err)
	}
	call := map[string]string{
		"to":   opStackGasPriceOracle,
		"data": "0x" + opStackGetL1FeeSelector + abiEncodeBytes(unsignedRLP),
	}
	var hexFee string
	if err := a.rpc.Call(ctx, "eth_call", &hexFee, call, "latest"); err != nil {
		return nil, fmt.Errorf("%w: L1 data fee on %s: %w", chain.ErrGasEstimateFailed, a.cfg.ChainIDStr, err)
	}
	fee := hexToBigInt(hexFee)
	if fee.Sign() <= 0 {
		return nil, fmt.Errorf("%w: GasPriceOracle on %s quoted no L1 data fee", chain.ErrGasEstimateFailed, a.cfg.ChainIDStr)
	}
	return fee.Mul(fee, big.NewInt(evmL1DataFeeMultiplier)), nil
}

// feeProbeTransaction is the unsigned transaction req would encode, with the
// fields BuildTransfer fills from the node (nonce) or the caller (amount when
// unknown) set to wide probe values.
func feeProbeTransaction(req types.TransferRequest, gasLimit uint64, gasPrice *big.Int) *gethtypes.Transaction {
	amount := req.Amount
	if amount == nil || amount.Sign() <= 0 {
		amount = evmFeeProbeAmount
	}
	recipient := probeAddressIfInvalid(req.To)
	if req.Token != nil {
		return gethtypes.NewTransaction(evmFeeProbeNonce, probeAddressIfInvalid(req.Token.Contract), new(big.Int),
			gasLimit, gasPrice, encodeERC20Transfer(recipient.Hex(), amount))
	}
	return gethtypes.NewTransaction(evmFeeProbeNonce, recipient, amount, gasLimit, gasPrice, nil)
}

func probeAddressIfInvalid(address string) common.Address {
	if !common.IsHexAddress(address) {
		return evmFeeProbeAddress
	}
	return common.HexToAddress(address)
}

// abiEncodeBytes is the ABI encoding of a single dynamic `bytes` argument.
func abiEncodeBytes(data []byte) string {
	padded := (len(data) + abiWordBytes - 1) / abiWordBytes * abiWordBytes
	encoded := make([]byte, 2*abiWordBytes+padded)
	big.NewInt(abiWordBytes).FillBytes(encoded[:abiWordBytes])
	big.NewInt(int64(len(data))).FillBytes(encoded[abiWordBytes : 2*abiWordBytes])
	copy(encoded[2*abiWordBytes:], data)
	return hex.EncodeToString(encoded)
}
