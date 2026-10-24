package solana

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/solanacredit"
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
	if block.Blockhash == "" {
		return nil, fmt.Errorf("solana %s: no endpoint returned slot %d", a.cfg.ChainIDStr, slot)
	}
	return solanaBlockCredits(slot, &block, a.cfg.NativeSymbol, a.tokens)
}

// isSolanaSkippedSlot reports a getBlock answer that the slot was skipped: the
// code and a message that says "skipped" (a provider answering its request limit
// with -32007 is not a skipped slot).
func isSolanaSkippedSlot(err error) bool {
	var rpcErr *rpc.RPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	return (rpcErr.Code == solanaRPCSlotSkipped || rpcErr.Code == solanaRPCSlotSkippedLongTermStorage) &&
		strings.Contains(strings.ToLower(rpcErr.Message), "skipped")
}

// solanaBlockCredits returns the credits of every successful transaction of the
// block, in block order (solanaTransactionCredits). A registered token balance that
// cannot be read fails the block.
func solanaBlockCredits(slot uint64, block *solanaBlockAccounts, nativeAsset string, tokens map[string]types.Token) ([]types.DetectedTransfer, error) {
	at := solanaSlotOf(slot, block.Blockhash, block.BlockTime)
	var transfers []types.DetectedTransfer
	for _, entry := range block.Transactions {
		var signature string
		if len(entry.Transaction.Signatures) > 0 {
			signature = entry.Transaction.Signatures[0]
		}
		keys := make([]string, len(entry.Transaction.AccountKeys))
		for i, key := range entry.Transaction.AccountKeys {
			keys[i] = key.Pubkey
		}
		credits, err := solanaTransactionCredits(at, solanaBalanceTransaction{Signature: signature, Keys: keys, Meta: entry.Meta}, nativeAsset, tokens)
		if err != nil {
			return nil, err
		}
		transfers = append(transfers, credits...)
	}
	return transfers, nil
}

// solanaSlot is where a transaction was read: its slot, the slot's blockhash (empty
// when the reader does not have it) and its block time (zero when the node has none).
type solanaSlot struct {
	slot      uint64
	blockhash string
	blockTime time.Time
}

func solanaSlotOf(slot uint64, blockhash string, blockTime *int64) solanaSlot {
	at := solanaSlot{slot: slot, blockhash: blockhash}
	if blockTime != nil {
		at.blockTime = time.Unix(*blockTime, 0).UTC()
	}
	return at
}

// solanaBalanceTransaction is one transaction as its balances read it: its first
// signature, its account keys in balance order (static, then the lookup-table loaded
// writable ones, then the readonly ones) and its meta.
type solanaBalanceTransaction struct {
	Signature string
	Keys      []string
	Meta      *solanaTransactionMeta
}

// solanaTransactionCredits returns the credits of one transaction: its lamport credits
// (account order, credited from the fee payer), then its credits of the registered
// tokens keyed by mint. The block scan and the signature sync both read a transaction
// through it, so they report the same credits at the same positions. A failed
// transaction only moved fees and credits nothing; one whose balances do not line up
// with its keys is logged and skipped. A registered token balance that cannot be read
// is an error.
func solanaTransactionCredits(at solanaSlot, tx solanaBalanceTransaction, nativeAsset string, tokens map[string]types.Token) ([]types.DetectedTransfer, error) {
	meta, keys := tx.Meta, tx.Keys
	if meta == nil || meta.Err != nil || tx.Signature == "" || len(keys) == 0 {
		return nil, nil
	}
	if len(meta.PreBalances) != len(keys) || len(meta.PostBalances) != len(keys) {
		slog.Warn("solana block: balances do not line up with account keys", "slot", at.slot, "tx", tx.Signature)
		return nil, nil
	}
	var transfers []types.DetectedTransfer
	credit := func(from, to string, amount *big.Int, asset string, token *types.Token, logIndex int) {
		transfers = append(transfers, types.DetectedTransfer{
			TxHash:      tx.Signature,
			BlockNumber: at.slot,
			BlockHash:   at.blockhash,
			From:        from,
			To:          to,
			Amount:      amount,
			Asset:       asset,
			Token:       token,
			LogIndex:    logIndex,
			Timestamp:   at.blockTime,
		})
	}
	feePayer := keys[0]
	lamports := make([]*big.Int, len(keys))
	for i := range keys {
		lamports[i] = new(big.Int).Sub(new(big.Int).SetUint64(meta.PostBalances[i]), new(big.Int).SetUint64(meta.PreBalances[i]))
	}
	for _, c := range solanacredit.Lamports(keys, lamports) {
		credit(feePayer, c.Account, c.Amount, nativeAsset, nil, c.Index)
	}
	tokenCredits, err := solanaTokenCredits(meta, len(keys), tokens)
	if err != nil {
		return nil, fmt.Errorf("sol slot %d tx %s: %w", at.slot, tx.Signature, err)
	}
	for _, c := range tokenCredits {
		from := c.from
		if from == "" {
			from = feePayer
		}
		token := c.token
		credit(from, c.owner, c.amount, token.Symbol, &token, solanacredit.TokenPosition(c.accountIndex))
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
