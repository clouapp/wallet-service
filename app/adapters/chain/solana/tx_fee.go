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

var _ chain.AccountRentReader = (*SolanaLive)(nil)

// TransactionAccountRent is the lamports the finalized transaction moved into the
// token accounts it created (an SPL transfer that opens the recipient's associated
// token account): the rent its fee payer paid beside meta.fee. A token account it
// created is one postTokenBalances lists and preTokenBalances does not; its rent is
// what its lamports rose by. A SOL transfer to a new system account creates no token
// account: its lamports are the amount sent, not rent.
func (a *SolanaLive) TransactionAccountRent(ctx context.Context, txHash string) (*big.Int, error) {
	if txHash == "" {
		return nil, fmt.Errorf("sol transaction signature is required")
	}
	var tx *struct {
		Meta *struct {
			PreBalances      []uint64 `json:"preBalances"`
			PostBalances     []uint64 `json:"postBalances"`
			PreTokenBalances []struct {
				AccountIndex int `json:"accountIndex"`
			} `json:"preTokenBalances"`
			PostTokenBalances []struct {
				AccountIndex int `json:"accountIndex"`
			} `json:"postTokenBalances"`
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
	meta := tx.Meta
	existed := make(map[int]bool, len(meta.PreTokenBalances))
	for _, balance := range meta.PreTokenBalances {
		existed[balance.AccountIndex] = true
	}
	rent := new(big.Int)
	for _, balance := range meta.PostTokenBalances {
		i := balance.AccountIndex
		if existed[i] {
			continue
		}
		if i < 0 || i >= len(meta.PreBalances) || i >= len(meta.PostBalances) {
			return nil, fmt.Errorf("sol transaction %s: token account %d has no lamport balance", txHash, i)
		}
		if meta.PostBalances[i] > meta.PreBalances[i] {
			rent.Add(rent, new(big.Int).SetUint64(meta.PostBalances[i]-meta.PreBalances[i]))
		}
		existed[i] = true
	}
	return rent, nil
}
