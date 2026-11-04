package evm

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

var _ chain.SwapCallBuilder = (*EVMLive)(nil)

// maxSwapCallGas bounds one provider call before the 25 % margin: the LI.FI and
// SwapKit routes read on 2026-11-04 estimated 0.2M-0.8M gas. A call the node
// estimates above it is refused rather than paid.
const maxSwapCallGas uint64 = 3_000_000

// PrepareSwapCall estimates execution from the real sender and fails closed if
// the node refuses simulation. The adapter obtains its own nonce and gas price.
func (a *EVMLive) PrepareSwapCall(ctx context.Context, from, to string, value *big.Int, data []byte) (*types.UnsignedTx, error) {
	if !common.IsHexAddress(from) || !common.IsHexAddress(to) || value == nil || value.Sign() < 0 || len(data) > 512*1024 {
		return nil, chain.InvalidAddress(fmt.Errorf("invalid swap call"))
	}
	call := map[string]string{"from": from, "to": to, "value": "0x" + value.Text(16), "data": "0x" + hex.EncodeToString(data)}
	var estimate string
	if err := a.rpc.Call(ctx, "eth_estimateGas", &estimate, call); err != nil {
		return nil, fmt.Errorf("%w: swap call: %w", chain.ErrGasEstimateFailed, err)
	}
	gas := hexToUint64(estimate)
	if gas == 0 || gas > maxSwapCallGas {
		return nil, chain.ErrGasEstimateFailed
	}
	gas = gas * 125 / 100
	nonce, err := a.pendingNonce(ctx, from)
	if err != nil {
		return nil, err
	}
	price, err := a.EstimateGasPrice(ctx)
	if err != nil {
		return nil, err
	}
	return a.BuildCall(chain.EVMCall{Nonce: nonce, To: to, Value: value, Data: data, GasLimit: gas, GasPrice: price})
}

// SwapAllowance reads only the allowance for the exact token, owner and spender.
func (a *EVMLive) SwapAllowance(ctx context.Context, token, owner, spender string) (*big.Int, error) {
	if !common.IsHexAddress(token) || !common.IsHexAddress(owner) || !common.IsHexAddress(spender) {
		return nil, fmt.Errorf("invalid allowance address")
	}
	data := append([]byte{0xdd, 0x62, 0xed, 0x3e}, common.LeftPadBytes(common.HexToAddress(owner).Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(common.HexToAddress(spender).Bytes(), 32)...)
	var result string
	if err := a.rpc.Call(ctx, "eth_call", &result, map[string]string{"to": token, "data": "0x" + hex.EncodeToString(data)}, "latest"); err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(result, "0x"))
	if err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("invalid allowance result")
	}
	return new(big.Int).SetBytes(decoded), nil
}
