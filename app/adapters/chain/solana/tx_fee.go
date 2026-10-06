package solana

import (
	"context"
	"fmt"
	"math/big"

	"github.com/macrowallets/waas/app/services/chain"
)

// TransactionFee is meta.fee (lamports) of the finalized transaction.

func (a *SolanaLive) TransactionFee(ctx context.Context, txHash string) (*big.Int, error) {
	if txHash == "" {
		return nil, fmt.Errorf("sol transaction signature is required")
	}
	var tx *struct {
		Meta *struct {
			Fee uint64 `json:"fee"`
		} `json:"meta"`
	}
	if err := a.rpc.Call(ctx, "getTransaction", &tx, txHash, map[string]any{
		"encoding": "json", "commitment": solanaCommitmentFinalized, "maxSupportedTransactionVersion": 0,
	}); err != nil {
		return nil, err
	}
	if tx == nil || tx.Meta == nil {
		return nil, fmt.Errorf("sol transaction %s: %w", txHash, chain.ErrTransactionFeeUnknown)
	}
	return new(big.Int).SetUint64(tx.Meta.Fee), nil
}
