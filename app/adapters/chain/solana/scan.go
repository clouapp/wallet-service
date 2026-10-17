package solana

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// Returned by getBlock for slots that produced no block; they will never have one.
	solanaRPCSlotSkipped                = -32007
	solanaRPCSlotSkippedLongTermStorage = -32009
	// getBlock and getTransaction refuse (-32015) a block or transaction that holds a
	// version newer than this. Devnet carries version-1 transactions since 2026-10;
	// the accounts encoding lists their keys and balances like legacy and v0 ones.
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
	Meta *solanaTransactionMeta `json:"meta"`
}

// solanaTransactionMeta is the status and the balances of one transaction, before and
// after it: lamports per account key, and the SPL token accounts it touched.
type solanaTransactionMeta struct {
	Err               interface{}          `json:"err"`
	PreBalances       []uint64             `json:"preBalances"`
	PostBalances      []uint64             `json:"postBalances"`
	PreTokenBalances  []solanaTokenBalance `json:"preTokenBalances"`
	PostTokenBalances []solanaTokenBalance `json:"postTokenBalances"`
}

// solanaBlockAccounts is getBlock with transactionDetails "accounts": account keys
// (static and lookup-table loaded, in balance order), lamport and token balances,
// without instructions, which keeps busy blocks under solanaRPCMaxResponseBytes.
type solanaBlockAccounts struct {
	Blockhash    string                   `json:"blockhash"`
	BlockTime    *int64                   `json:"blockTime"`
	Transactions []solanaBlockTransaction `json:"transactions"`
}

// ScanBlock returns every native SOL credit and every credit of a registered SPL token
// in a finalized slot. A skipped slot has no block and yields no transfers, so the
// scanner checkpoint moves past it. A registered mint that is not a public key fails
// the scan, so the block is retried instead of marked scanned without its deposits.
func (a *SolanaLive) ScanBlock(ctx context.Context, slot uint64) ([]types.DetectedTransfer, error) {
	if a.tokensErr != nil {
		return nil, a.tokensErr
	}
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
	return solanaBlockCredits(slot, &block, a.cfg.NativeSymbol, a.tokens)
}

func isSolanaSkippedSlot(err error) bool {
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	return rpcErr.Code == solanaRPCSlotSkipped || rpcErr.Code == solanaRPCSlotSkippedLongTermStorage
}

// solanaBlockCredits returns, per successful transaction in block order, its lamport
// credits (account order, credited from the fee payer) and then its credits of the
// tokens keyed by mint. Failed transactions only move fees. A registered token balance
// that cannot be read fails the block.
func solanaBlockCredits(slot uint64, block *solanaBlockAccounts, nativeAsset string, tokens map[string]types.Token) ([]types.DetectedTransfer, error) {
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
		credit := func(from, to string, amount *big.Int, asset string, token *types.Token, logIndex int) {
			transfers = append(transfers, types.DetectedTransfer{
				TxHash:      signature,
				BlockNumber: slot,
				BlockHash:   block.Blockhash,
				From:        from,
				To:          to,
				Amount:      amount,
				Asset:       asset,
				Token:       token,
				LogIndex:    uint(logIndex),
				Timestamp:   blockTime,
			})
		}
		feePayer := keys[0].Pubkey
		for i, key := range keys {
			pre, post := meta.PreBalances[i], meta.PostBalances[i]
			if post <= pre || key.Pubkey == feePayer {
				continue
			}
			credit(feePayer, key.Pubkey, new(big.Int).SetUint64(post-pre), nativeAsset, nil, i)
		}
		tokenCredits, err := solanaTokenCredits(meta, len(keys), tokens)
		if err != nil {
			return nil, fmt.Errorf("sol slot %d tx %s: %w", slot, signature, err)
		}
		for _, c := range tokenCredits {
			from := c.from
			if from == "" {
				from = feePayer
			}
			token := c.token
			// Token credits are numbered after the account keys, so they never share a
			// log index with a lamport credit of the same transaction.
			credit(from, c.owner, c.amount, token.Symbol, &token, len(keys)+c.accountIndex)
		}
	}
	return transfers, nil
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
		raw := fmt.Sprint(status.Err)
		return 0, chain.Wrap(chain.KindOrProvider(0, raw), fmt.Errorf("sol transaction %s failed on-chain: %s", txHash, raw))
	}
	if status.ConfirmationStatus != solanaCommitmentFinalized {
		return 0, nil
	}
	return status.Slot, nil
}
