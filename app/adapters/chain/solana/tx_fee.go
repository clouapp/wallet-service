package solana

import (
	"context"
	"fmt"
	"math/big"

	"github.com/macrowallets/waas/app/services/chain"
)

// TransactionFee is meta.fee (lamports) of the finalized transaction. It asks for the
// newest transaction version the adapter reads: devnet refuses a version-1
// transaction to a request that allows only version 0.
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
		"encoding": "json", "commitment": solanaCommitmentFinalized, "maxSupportedTransactionVersion": solanaMaxSupportedTxVersion,
	}); err != nil {
		return nil, err
	}
	if tx == nil || tx.Meta == nil {
		return nil, fmt.Errorf("sol transaction %s: %w", txHash, chain.ErrTransactionFeeUnknown)
	}
	return new(big.Int).SetUint64(tx.Meta.Fee), nil
}
