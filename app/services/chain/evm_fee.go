package chain

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"

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
	// evmArbitrumNativeGasMarginPercent pads Arbitrum's native transfer estimate,
	// whose L1 component follows the L1 price.
	evmArbitrumNativeGasMarginPercent = uint64(150)
	abiWordBytes                      = 32
)

// Representative transfer used to size fees before the real transfer is known.
// Its fields are as wide as a real transfer's so the L1 data fee is not undersized.
var (
	evmFeeProbeAddress = common.HexToAddress("0x1111111111111111111111111111111111111111")
	evmFeeProbeNonce   = uint64(1<<32 - 1)
	evmFeeProbeAmount  = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
)

func (a *EVMLive) feeModel() models.EVMFeeModel {
	return models.EVMFeeModelOf(a.cfg.NetworkID)
}

// NativeTransferGasLimit is the gas limit BuildTransfer encodes for a native
// transfer from → to. It is the fixed 21000 except on Arbitrum, whose L1 cost is
// billed as extra L2 gas: there the node estimates a zero-value transfer (value
// does not change an EOA transfer's gas, and a full-balance value would fail the
// estimate) and the estimate is padded by evmArbitrumNativeGasMarginPercent.
// Empty addresses use a probe address.
func (a *EVMLive) NativeTransferGasLimit(ctx context.Context, from, to string) (uint64, error) {
	if a.feeModel() != models.EVMFeeModelArbitrum {
		return evmNativeTransferGasLimit, nil
	}
	call := map[string]string{
		"from":  probeIfEmpty(from),
		"to":    probeIfEmpty(to),
		"value": "0x0",
	}
	var hexEstimate string
	if err := a.rpc.Call(ctx, "eth_estimateGas", &hexEstimate, call); err != nil {
		return 0, fmt.Errorf("%w: native transfer from %s on %s: %v", ErrGasEstimateFailed, call["from"], a.cfg.ChainIDStr, err)
	}
	estimate := hexToUint64(hexEstimate)
	if estimate == 0 {
		return 0, fmt.Errorf("%w: node returned no gas for a native transfer on %s", ErrGasEstimateFailed, a.cfg.ChainIDStr)
	}
	padded := estimate * evmArbitrumNativeGasMarginPercent / percentDenominator
	return max(padded, evmNativeTransferGasLimit), nil
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
		gasLimit = evmNativeTransferGasLimit
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
		return nil, fmt.Errorf("%w: L1 data fee on %s: %v", ErrGasEstimateFailed, a.cfg.ChainIDStr, err)
	}
	fee := hexToBigInt(hexFee)
	if fee.Sign() <= 0 {
		return nil, fmt.Errorf("%w: GasPriceOracle on %s quoted no L1 data fee", ErrGasEstimateFailed, a.cfg.ChainIDStr)
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

func probeIfEmpty(address string) string {
	if address == "" {
		return evmFeeProbeAddress.Hex()
	}
	return address
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
