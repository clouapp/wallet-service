package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/pkg/types"
)

// ErrDepositReceiptMissing is a deposit whose transaction the node returns no
// receipt for: not mined in any block the node knows (reorganized away, or a
// lagging node). It is neither confirmed nor failed.
var ErrDepositReceiptMissing = errors.New("deposit: the node has no receipt for the transaction")

// receiptBlock is the native transfers to watched addresses of one block.
type receiptBlock struct {
	number   uint64
	hash     string
	txHashes []string
}

// withoutFailedNativeTransfers returns the transfers (all to watched addresses)
// that may be recorded as deposits. On a chain whose blocks include failed
// transactions (chain.ReceiptStatusReader: EVM), a block lists a transaction mined
// with receipt status 0x0 with its value although none of it moved, so every
// native transfer is kept only when its receipt succeeded; a failed one is dropped
// and logged. Receipts are read only for these candidates, once per block. A
// receipt that cannot be read fails the block: its native transfers are neither
// recorded nor dropped, they are returned as unverified with the error, so the
// scanner retries the block and then keeps it pending. Token transfers come from
// Transfer logs, which a failed transaction never emits, and are always kept.
func (s *Service) withoutFailedNativeTransfers(ctx context.Context, chainID string, adapter types.Chain, transfers []types.DetectedTransfer) ([]types.DetectedTransfer, []string, error) {
	reader, ok := adapter.(chain.ReceiptStatusReader)
	if !ok {
		return transfers, nil, nil
	}
	blocks := nativeTransferBlocks(transfers)
	if len(blocks) == 0 {
		return transfers, nil, nil
	}
	succeeded := make(map[string]bool)
	var unverified []string
	var failures []error
	for _, block := range blocks {
		statuses, err := reader.TransactionsSucceeded(ctx, block.number, block.hash, block.txHashes)
		if err != nil {
			unverified = append(unverified, block.txHashes...)
			failures = append(failures, classify(pending.ClassRPC, fmt.Errorf("read receipts of block %d: %w", block.number, err)))
			continue
		}
		for _, hash := range block.txHashes {
			status, known := statuses[strings.ToLower(hash)]
			if !known {
				unverified = append(unverified, hash)
				failures = append(failures, classify(pending.ClassRPC, fmt.Errorf("receipts of block %d do not report tx %s", block.number, hash)))
				continue
			}
			succeeded[strings.ToLower(hash)] = status
		}
	}
	kept := make([]types.DetectedTransfer, 0, len(transfers))
	for _, transfer := range transfers {
		if transfer.Token != nil {
			kept = append(kept, transfer)
			continue
		}
		status, verified := succeeded[strings.ToLower(transfer.TxHash)]
		if !verified {
			continue
		}
		if !status {
			slog.Warn("native transfer to a watched address failed on chain (receipt status 0x0): its value did not move, not a deposit",
				"chain", chainID, "tx", transfer.TxHash, "block", transfer.BlockNumber, "to", transfer.To, "amount", transfer.Amount.String())
			continue
		}
		kept = append(kept, transfer)
	}
	return kept, unverified, errors.Join(failures...)
}

// nativeTransferBlocks groups the native transfers by block, in the order the
// blocks first appear.
func nativeTransferBlocks(transfers []types.DetectedTransfer) []receiptBlock {
	var blocks []receiptBlock
	index := make(map[uint64]int)
	for _, transfer := range transfers {
		if transfer.Token != nil {
			continue
		}
		i, seen := index[transfer.BlockNumber]
		if !seen {
			i = len(blocks)
			index[transfer.BlockNumber] = i
			blocks = append(blocks, receiptBlock{number: transfer.BlockNumber, hash: transfer.BlockHash})
		}
		blocks[i].txHashes = append(blocks[i].txHashes, transfer.TxHash)
	}
	return blocks
}

// checksDepositReceipt reports whether tx is a native deposit on a chain whose
// blocks include failed transactions, so its receipt is re-read before it is
// confirmed. A token deposit comes from a Transfer log, which a failed transaction
// never emits.
func checksDepositReceipt(adapter types.Chain, tx models.Transaction) bool {
	if tx.TxType != models.TxTypeDeposit || strings.TrimSpace(tx.TokenContract) != "" {
		return false
	}
	_, ok := adapter.(chain.ReceiptStatusReader)
	return ok
}

// nativeDepositReceipt reads the receipt of a native deposit's transaction
// (GetTransactionBlock): the on-chain failure code (onchain_out_of_gas,
// onchain_reverted) when it was mined and failed, empty when it succeeded, or an
// error when the receipt could not be read or the node has none
// (ErrDepositReceiptMissing).
func nativeDepositReceipt(ctx context.Context, adapter types.Chain, tx models.Transaction) (string, error) {
	block, err := adapter.GetTransactionBlock(ctx, tx.TxHash)
	var onChain *chain.TransactionFailedError
	if errors.As(err, &onChain) {
		return onChainFailureCode(onChain.Result), nil
	}
	if err != nil {
		return "", fmt.Errorf("read receipt of tx %s: %w", tx.TxHash, err)
	}
	if block == 0 {
		return "", fmt.Errorf("tx %s: %w", tx.TxHash, ErrDepositReceiptMissing)
	}
	return "", nil
}

// depositReceiptAllowsConfirmation re-reads, right before the tracker confirms it,
// the receipt of a native deposit that checksDepositReceipt selects; it also
// catches deposits recorded before the scanner read receipts and deposits from the
// inbound ingest. Only a receipt that succeeded lets the deposit confirm. A failed
// receipt fails the deposit and sends deposit.failed instead. A receipt that cannot
// be read, or that the node does not have, leaves the deposit unconfirmed until a
// later pass reads it.
func (s *Service) depositReceiptAllowsConfirmation(ctx context.Context, adapter types.Chain, tx models.Transaction) bool {
	failureCode, err := nativeDepositReceipt(ctx, adapter, tx)
	if err != nil {
		slog.Warn("deposit not confirmed this pass: its receipt was not verified", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "error", err)
		return false
	}
	if failureCode == "" {
		return true
	}
	if err := s.failDeposit(ctx, tx, failureCode); err != nil {
		slog.Error("deposit whose receipt failed was not marked failed; retrying next pass", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "error", err)
	}
	return false
}

// failDeposit records a deposit whose transaction was mined and failed, so no
// value reached the address: it publishes deposit.failed with failureCode, then
// marks the row failed, last, so a failed publish leaves the row as it was and the
// next pass (or run) tries again. The outbox keeps one deposit.failed per config
// and deposit, so a retry never sends it twice. Without a deposit events publisher
// the row is still marked failed, and no deposit webhook is sent at all.
func (s *Service) failDeposit(ctx context.Context, tx models.Transaction, failureCode string) error {
	tx.Status = string(types.TxStatusFailed)
	tx.ErrorMessage = failureCode
	if s.deposits == nil {
		slog.Error("deposit webhook not sent: no deposit events publisher configured", "event_type", types.EventDepositFailed, "transaction_id", tx.ID)
	} else if err := s.deposits.Publish(ctx, types.EventDepositFailed, tx); err != nil {
		return fmt.Errorf("publish %s for deposit %s: %w", types.EventDepositFailed, tx.ID, err)
	}
	if err := s.txRepo.MarkFailed(ctx, tx.ID, failureCode); err != nil {
		return fmt.Errorf("mark deposit %s failed: %w", tx.ID, err)
	}
	slog.Warn("deposit failed on chain: its transaction was mined without moving its value",
		"tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "to", tx.ToAddress, "amount", tx.Amount, "failure_code", failureCode)
	return nil
}
