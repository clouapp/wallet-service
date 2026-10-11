package xrp

import (
	"context"
	"fmt"
	"math/big"

	"github.com/macrowallets/waas/app/services/chain"
)

var _ chain.TransactionFeeReader = (*Live)(nil)

// TransactionFee is the Fee field, in drops, of a transaction in a validated
// ledger. The fee is destroyed whether the payment succeeded or failed with a tec
// code. A transaction not validated yet is chain.ErrTransactionFeeUnknown.
func (a *Live) TransactionFee(ctx context.Context, txHash string) (*big.Int, error) {
	result, found, err := a.lookupTransaction(ctx, txHash)
	if err != nil {
		return nil, err
	}
	if !found || !result.Validated {
		return nil, fmt.Errorf("xrp tx %s: %w", txHash, chain.ErrTransactionFeeUnknown)
	}
	raw := result.Fee
	if len(raw) == 0 {
		raw = result.TxJSON.Fee
	}
	fee, err := parseDrops(raw)
	if err != nil {
		return nil, fmt.Errorf("xrp tx %s fee: %w", txHash, err)
	}
	return fee, nil
}
