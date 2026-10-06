package evm

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/macrowallets/waas/app/services/chain"
)

// evmFeeReceipt is the part of an eth_getTransactionReceipt the paid fee is read from.
type evmFeeReceipt struct {
	GasUsed           string `json:"gasUsed"`
	EffectiveGasPrice string `json:"effectiveGasPrice"`
	L1Fee             string `json:"l1Fee"`
}

// TransactionFee is gasUsed × effectiveGasPrice, plus the L1 data fee OP Stack
// chains (Base) report as l1Fee. Arbitrum folds its L1 cost into gasUsed.
func (a *EVMLive) TransactionFee(ctx context.Context, txHash string) (*big.Int, error) {
	var receipt *evmFeeReceipt
	if err := a.rpc.Call(ctx, "eth_getTransactionReceipt", &receipt, txHash); err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, fmt.Errorf("evm receipt of %s: %w", txHash, chain.ErrTransactionFeeUnknown)
	}
	gasUsed, err := parseHexQuantity(receipt.GasUsed, "gasUsed")
	if err != nil {
		return nil, fmt.Errorf("evm receipt of %s: %w", txHash, err)
	}
	priceHex := receipt.EffectiveGasPrice
	if priceHex == "" {
		var tx *struct {
			GasPrice string `json:"gasPrice"`
		}
		if err := a.rpc.Call(ctx, "eth_getTransactionByHash", &tx, txHash); err != nil {
			return nil, err
		}
		if tx == nil {
			return nil, fmt.Errorf("evm transaction %s: %w", txHash, chain.ErrTransactionFeeUnknown)
		}
		priceHex = tx.GasPrice
	}
	price, err := parseHexQuantity(priceHex, "gas price")
	if err != nil {
		return nil, fmt.Errorf("evm receipt of %s: %w", txHash, err)
	}
	fee := new(big.Int).Mul(gasUsed, price)
	if receipt.L1Fee != "" {
		l1Fee, err := parseHexQuantity(receipt.L1Fee, "l1Fee")
		if err != nil {
			return nil, fmt.Errorf("evm receipt of %s: %w", txHash, err)
		}
		fee.Add(fee, l1Fee)
	}
	return fee, nil
}

func parseHexQuantity(value, field string) (*big.Int, error) {
	digits := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "0x"), "0X")
	if digits == "" {
		return nil, fmt.Errorf("%s is empty", field)
	}
	n, ok := new(big.Int).SetString(digits, 16)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("%s %q is not a hex quantity", field, value)
	}
	return n, nil
}
