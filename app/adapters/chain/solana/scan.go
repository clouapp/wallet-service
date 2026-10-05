package solana

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// Returned by getBlock for slots that produced no block; they will never have one.
	solanaRPCSlotSkipped                = -32007
	solanaRPCSlotSkippedLongTermStorage = -32009
	// getBlock rejects the whole block when it holds a newer transaction version.
	solanaMaxSupportedTxVersion = 1
	solanaCommitmentFinalized   = "finalized"
)

type solanaBlockAccountKey struct {
	Pubkey string `json:"pubkey"`
	Signer bool   `json:"signer"`
}

type solanaBlockTransaction struct {
	Transaction struct {
		AccountKeys []solanaBlockAccountKey `json:"accountKeys"`
		Signatures  []string                `json:"signatures"`
	} `json:"transaction"`
	Meta *struct {
		Err          interface{} `json:"err"`
		PreBalances  []uint64    `json:"preBalances"`
		PostBalances []uint64    `json:"postBalances"`
	} `json:"meta"`
}

// solanaBlockAccounts is getBlock with transactionDetails "accounts": account keys
// (static and lookup-table loaded, in balance order) and balances, without
// instructions, which keeps busy blocks well under the RPC body limit.
type solanaBlockAccounts struct {
	Blockhash    string                   `json:"blockhash"`
	BlockTime    *int64                   `json:"blockTime"`
	Transactions []solanaBlockTransaction `json:"transactions"`
}

// ScanBlock returns every native SOL credit in a finalized slot. A skipped slot has
// no block and yields no transfers, so the scanner checkpoint moves past it.
func (a *SolanaLive) ScanBlock(ctx context.Context, slot uint64) ([]types.DetectedTransfer, error) {
	var block solanaBlockAccounts
	err := a.rpc.Call(ctx, "getBlock", &block, slot, map[string]interface{}{
		"encoding":                       "json",
		"transactionDetails":             "accounts",
		"rewards":                        false,
		"commitment":                     solanaCommitmentFinalized,
		"maxSupportedTransactionVersion": solanaMaxSupportedTxVersion,
	})
	if isSolanaSkippedSlot(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return solanaNativeCredits(slot, &block, a.cfg.NativeSymbol), nil
}

func isSolanaSkippedSlot(err error) bool {
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	return rpcErr.Code == solanaRPCSlotSkipped || rpcErr.Code == solanaRPCSlotSkippedLongTermStorage
}

// solanaNativeCredits turns balance increases of successful transactions into
// transfers credited from the fee payer. Failed transactions only move fees.
func solanaNativeCredits(slot uint64, block *solanaBlockAccounts, asset string) []types.DetectedTransfer {
	var blockTime time.Time
	if block.BlockTime != nil {
		blockTime = time.Unix(*block.BlockTime, 0).UTC()
	}

	var transfers []types.DetectedTransfer
	for _, entry := range block.Transactions {
		meta := entry.Meta
		keys := entry.Transaction.AccountKeys
		if meta == nil || meta.Err != nil || len(entry.Transaction.Signatures) == 0 || len(keys) == 0 {
			continue
		}
		signature := entry.Transaction.Signatures[0]
		if len(meta.PreBalances) != len(keys) || len(meta.PostBalances) != len(keys) {
			slog.Warn("solana block: balances do not line up with account keys", "slot", slot, "tx", signature)
			continue
		}
		feePayer := keys[0].Pubkey
		for i, key := range keys {
			pre, post := meta.PreBalances[i], meta.PostBalances[i]
			if post <= pre || key.Pubkey == feePayer {
				continue
			}
			transfers = append(transfers, types.DetectedTransfer{
				TxHash:      signature,
				BlockNumber: slot,
				BlockHash:   block.Blockhash,
				From:        feePayer,
				To:          key.Pubkey,
				Amount:      new(big.Int).SetUint64(post - pre),
				Asset:       asset,
				LogIndex:    uint(i),
				Timestamp:   blockTime,
			})
		}
	}
	return transfers
}

type solanaSignatureStatus struct {
	Slot               uint64      `json:"slot"`
	Err                interface{} `json:"err"`
	ConfirmationStatus string      `json:"confirmationStatus"`
}

// GetTransactionBlock returns the slot of a finalized, successful transaction, or 0
// while it is unknown or not yet finalized. A transaction that failed on-chain
// returns an error so it is never counted as a confirmed transfer.
func (a *SolanaLive) GetTransactionBlock(ctx context.Context, txHash string) (uint64, error) {
	if txHash == "" {
		return 0, fmt.Errorf("sol transaction signature is required")
	}
	var result struct {
		Value []*solanaSignatureStatus `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getSignatureStatuses", &result, []string{txHash},
		map[string]bool{"searchTransactionHistory": true}); err != nil {
		return 0, err
	}
	if len(result.Value) == 0 || result.Value[0] == nil {
		return 0, nil
	}
	status := result.Value[0]
	if status.Err != nil {
		return 0, fmt.Errorf("sol transaction %s failed on-chain: %v", txHash, status.Err)
	}
	if status.ConfirmationStatus != solanaCommitmentFinalized {
		return 0, nil
	}
	return status.Slot, nil
}
