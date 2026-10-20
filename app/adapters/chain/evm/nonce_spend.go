package evm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// minSpendDepth is the fewest blocks under the head a spend of a nonce must be
// before it is believed: a node a few blocks behind may not know a block that was
// just mined; it is very unlikely to be this far behind.
const minSpendDepth = 12

// SenderNonce is the nonce the signed transaction spends.
func (a *EVMLive) SenderNonce(signed *types.SignedTx) (uint64, error) {
	if signed == nil || len(signed.RawBytes) == 0 {
		return 0, fmt.Errorf("evm sender nonce: signed transaction is required")
	}
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(signed.RawBytes); err != nil {
		return 0, fmt.Errorf("evm sender nonce: %w", err)
	}
	return tx.Nonce(), nil
}

// NonceSpend finds, by positive proof, the transaction that spent nonce of from
// (chain.NonceSpendReader). The search starts at the block max(confirmations, 12)
// under the head and walks back, doubling its step, to a block where the nonce was
// still free, so the newest blocks (which a node without archive state still has)
// are read first; a binary search then finds the block where the count passed
// nonce. That block's transaction of from with that nonce is ours (its receipt,
// read by block, gives its result: a node without a transaction index still has
// it) or another one. A nonce spent at or before fromBlock (the head when the
// transfer was sent) is not this transfer's spend and is an error, as is any read
// that fails: nothing is proven.
func (a *EVMLive) NonceSpend(ctx context.Context, txHash, from string, nonce, fromBlock uint64) (chain.NonceSpend, error) {
	if strings.TrimSpace(txHash) == "" || strings.TrimSpace(from) == "" {
		return chain.NonceSpend{}, fmt.Errorf("evm nonce spend: tx hash and sender are required")
	}
	head, err := a.GetLatestBlock(ctx)
	if err != nil {
		return chain.NonceSpend{}, err
	}
	depth := max(a.cfg.Confirmations, minSpendDepth)
	if head < depth || head-depth <= fromBlock {
		return chain.NonceSpend{}, nil
	}
	spentAt := func(block uint64) (bool, error) {
		var count string
		if err := a.rpc.Call(ctx, "eth_getTransactionCount", &count, from, fmt.Sprintf("0x%x", block)); err != nil {
			if rpc.MissingState(err) {
				err = errors.Join(chain.ErrHistoricalStateUnavailable, err)
			}
			return false, fmt.Errorf("evm nonce spend: nonce count of %s at block %d (a node without that block's state needs an archive endpoint): %w", from, block, err)
		}
		spent, err := parseHexQuantity(count, "nonce count")
		if err != nil {
			return false, err
		}
		return !spent.IsUint64() || spent.Uint64() > nonce, nil
	}
	spentAbove := head - depth
	if spent, err := spentAt(spentAbove); err != nil || !spent {
		return chain.NonceSpend{}, err
	}
	var freeAt uint64
	for step := uint64(1); ; step *= 2 {
		probe := fromBlock
		if spentAbove > fromBlock+step {
			probe = spentAbove - step
		}
		spent, err := spentAt(probe)
		if err != nil {
			return chain.NonceSpend{}, err
		}
		if !spent {
			freeAt = probe
			break
		}
		if probe == fromBlock {
			return chain.NonceSpend{}, fmt.Errorf("evm nonce spend: nonce %d of %s was spent at or before block %d, when the transfer was sent", nonce, from, fromBlock)
		}
		spentAbove = probe
	}
	for spentAbove-freeAt > 1 {
		mid := freeAt + (spentAbove-freeAt)/2
		spent, err := spentAt(mid)
		if err != nil {
			return chain.NonceSpend{}, err
		}
		if spent {
			spentAbove = mid
		} else {
			freeAt = mid
		}
	}
	return a.spendInBlock(ctx, spentAbove, txHash, from, nonce)
}

// spendInBlock reads the transaction of from with nonce in block, which spent it.
func (a *EVMLive) spendInBlock(ctx context.Context, block uint64, txHash, from string, nonce uint64) (chain.NonceSpend, error) {
	hexBlock := fmt.Sprintf("0x%x", block)
	var full struct {
		Transactions []struct {
			Hash  string `json:"hash"`
			From  string `json:"from"`
			Nonce string `json:"nonce"`
			Gas   string `json:"gas"`
		} `json:"transactions"`
	}
	if err := a.rpc.Call(ctx, "eth_getBlockByNumber", &full, hexBlock, true); err != nil {
		return chain.NonceSpend{}, err
	}
	for _, tx := range full.Transactions {
		if !strings.EqualFold(tx.From, from) || hexToUint64(tx.Nonce) != nonce {
			continue
		}
		if !strings.EqualFold(tx.Hash, txHash) {
			return chain.NonceSpend{Spent: true, Block: block}, nil
		}
		result, err := a.receiptResultInBlock(ctx, hexBlock, txHash, tx.Gas)
		if err != nil {
			return chain.NonceSpend{}, err
		}
		return chain.NonceSpend{Spent: true, Block: block, Ours: true, Result: result}, nil
	}
	return chain.NonceSpend{}, fmt.Errorf("evm nonce spend: block %d spent nonce %d of %s but the node returns no such transaction in it", block, nonce, from)
}

// receiptResultInBlock is the failure result of txHash's receipt in hexBlock, read
// with the block's receipts (then by hash): "" when it succeeded.
func (a *EVMLive) receiptResultInBlock(ctx context.Context, hexBlock, txHash, gasLimit string) (string, error) {
	type receipt struct {
		TransactionHash string `json:"transactionHash"`
		Status          string `json:"status"`
		GasUsed         string `json:"gasUsed"`
	}
	var found *receipt
	var receipts []receipt
	if err := a.rpc.Call(ctx, "eth_getBlockReceipts", &receipts, hexBlock); err == nil {
		for i := range receipts {
			if strings.EqualFold(receipts[i].TransactionHash, txHash) {
				found = &receipts[i]
			}
		}
	}
	if found == nil {
		if err := a.rpc.Call(ctx, "eth_getTransactionReceipt", &found, txHash); err != nil {
			return "", err
		}
	}
	if found == nil {
		return "", fmt.Errorf("evm nonce spend: no receipt for %s in block %s", txHash, hexBlock)
	}
	switch strings.ToLower(found.Status) {
	case evmReceiptStatusSuccess:
		return "", nil
	case evmReceiptStatusFailure:
		used, err := parseHexQuantity(found.GasUsed, "gasUsed")
		if err != nil {
			return "", err
		}
		limit, err := parseHexQuantity(gasLimit, "gas")
		if err != nil {
			return "", err
		}
		if used.Cmp(limit) >= 0 {
			return evmResultOutOfGas, nil
		}
		return evmResultReverted, nil
	default:
		return "", fmt.Errorf("evm receipt of %s in block %s has status %q", txHash, hexBlock, found.Status)
	}
}
