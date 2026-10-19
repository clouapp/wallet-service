package evm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/macrowallets/waas/app/adapters/chain/rpc"
	"github.com/macrowallets/waas/app/services/chain"
)

var _ chain.ReceiptStatusReader = (*EVMLive)(nil)

// evmReceipt is the part of a transaction receipt that tells whether the
// transaction succeeded in the block the deposit scanner read.
type evmReceipt struct {
	TransactionHash string `json:"transactionHash"`
	BlockHash       string `json:"blockHash"`
	BlockNumber     string `json:"blockNumber"`
	Status          string `json:"status"`
}

// TransactionsSucceeded reads the receipts of transactions of block blockNum
// (chain.ReceiptStatusReader). One transaction reads its own receipt
// (eth_getTransactionReceipt, about 1 KB). Several transactions read the receipts
// of the whole block once (eth_getBlockReceipts, which is larger than the block
// itself but one call instead of one per transaction). A rejection of
// eth_getBlockReceipts with a JSON-RPC error is read one receipt at a time instead.
// An endpoint that refuses a block number twice in a row (-32602) is not asked it
// for 10 minutes while another endpoint can be (the RPC client remembers it for that
// endpoint only); "method not found" is never remembered. The RPC client retries rate-limited calls;
// an outage, a rate limit that outlasts the retries, a missing receipt, a receipt
// of another block (reorganized) or an unknown status fails the whole read.
func (a *EVMLive) TransactionsSucceeded(ctx context.Context, blockNum uint64, blockHash string, txHashes []string) (map[string]bool, error) {
	hashes := distinctLowerHashes(txHashes)
	if len(hashes) == 0 {
		return map[string]bool{}, nil
	}
	if len(hashes) > 1 {
		statuses, err := a.blockReceiptStatuses(ctx, blockNum, blockHash, hashes)
		if !blockReceiptsRejected(err) {
			return statuses, err
		}
	}
	return a.transactionReceiptStatuses(ctx, blockNum, blockHash, hashes)
}

func (a *EVMLive) blockReceiptStatuses(ctx context.Context, blockNum uint64, blockHash string, hashes []string) (map[string]bool, error) {
	var receipts []evmReceipt
	if err := a.rpc.Call(ctx, "eth_getBlockReceipts", &receipts, fmt.Sprintf("0x%x", blockNum)); err != nil {
		return nil, fmt.Errorf("eth_getBlockReceipts of block %d: %w", blockNum, err)
	}
	if receipts == nil {
		return nil, fmt.Errorf("evm node returned no receipts for block %d", blockNum)
	}
	byHash := make(map[string]evmReceipt, len(receipts))
	for _, receipt := range receipts {
		byHash[strings.ToLower(receipt.TransactionHash)] = receipt
	}
	statuses := make(map[string]bool, len(hashes))
	for _, hash := range hashes {
		receipt, ok := byHash[hash]
		if !ok {
			return nil, fmt.Errorf("receipts of block %d do not include evm tx %s", blockNum, hash)
		}
		succeeded, err := receiptSucceeded(receipt, blockNum, blockHash)
		if err != nil {
			return nil, fmt.Errorf("evm tx %s: %w", hash, err)
		}
		statuses[hash] = succeeded
	}
	return statuses, nil
}

func (a *EVMLive) transactionReceiptStatuses(ctx context.Context, blockNum uint64, blockHash string, hashes []string) (map[string]bool, error) {
	statuses := make(map[string]bool, len(hashes))
	for _, hash := range hashes {
		var receipt *evmReceipt
		if err := a.rpc.Call(ctx, "eth_getTransactionReceipt", &receipt, hash); err != nil {
			return nil, fmt.Errorf("eth_getTransactionReceipt of %s: %w", hash, err)
		}
		if receipt == nil {
			return nil, fmt.Errorf("evm tx %s of block %d has no receipt", hash, blockNum)
		}
		succeeded, err := receiptSucceeded(*receipt, blockNum, blockHash)
		if err != nil {
			return nil, fmt.Errorf("evm tx %s: %w", hash, err)
		}
		statuses[hash] = succeeded
	}
	return statuses, nil
}

// receiptSucceeded reads the status of a receipt that must belong to block blockNum
// (and to blockHash, when the scan knows it).
func receiptSucceeded(receipt evmReceipt, blockNum uint64, blockHash string) (bool, error) {
	number, err := parseHexQuantity(receipt.BlockNumber, "receipt blockNumber")
	if err != nil {
		return false, err
	}
	if !number.IsUint64() || number.Uint64() != blockNum {
		return false, fmt.Errorf("receipt is in block %s, the scan read block %d", number, blockNum)
	}
	if blockHash != "" && !strings.EqualFold(receipt.BlockHash, blockHash) {
		return false, fmt.Errorf("receipt is in block %d with hash %q, the scan read hash %q (reorganized)", blockNum, receipt.BlockHash, blockHash)
	}
	switch strings.ToLower(receipt.Status) {
	case evmReceiptStatusSuccess:
		return true, nil
	case evmReceiptStatusFailure:
		return false, nil
	default:
		return false, fmt.Errorf("receipt in block %d has status %q", blockNum, receipt.Status)
	}
}

// blockReceiptsRejected tells whether eth_getBlockReceipts was answered with a
// JSON-RPC error. An outage, a rate limit or an unreadable answer is not a rejection.
func blockReceiptsRejected(err error) bool {
	var rpcErr *rpc.RPCError
	return errors.As(err, &rpcErr)
}

func distinctLowerHashes(txHashes []string) []string {
	seen := make(map[string]bool, len(txHashes))
	hashes := make([]string, 0, len(txHashes))
	for _, hash := range txHashes {
		hash = strings.ToLower(strings.TrimSpace(hash))
		if hash == "" || seen[hash] {
			continue
		}
		seen[hash] = true
		hashes = append(hashes, hash)
	}
	return hashes
}
