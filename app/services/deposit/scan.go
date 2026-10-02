package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	DefaultScanBatchBlocks   = 50
	DefaultScanCatchUpBlocks = 500
	DefaultScanConcurrency   = 8
	MaxScanConcurrency       = 32
)

// ScanOptions bound how much of the chain one ScanLatestBlocks call covers. The
// checkpoint never jumps ahead: a scanner far behind the head catches up with larger,
// parallel windows instead, so no block between the checkpoint and the head is skipped.
type ScanOptions struct {
	// BatchBlocks is the window of a cycle whose checkpoint is near the head.
	BatchBlocks uint64
	// CatchUpBlocks is the window while the checkpoint trails the head by more than
	// BatchBlocks.
	CatchUpBlocks uint64
	// Concurrency is how many blocks are fetched at once; transfers are still recorded
	// in block order.
	Concurrency int
}

func DefaultScanOptions() ScanOptions {
	return ScanOptions{
		BatchBlocks:   DefaultScanBatchBlocks,
		CatchUpBlocks: DefaultScanCatchUpBlocks,
		Concurrency:   DefaultScanConcurrency,
	}
}

// ScanOptionsFromSettings builds options from configured values, where 0 keeps the
// default; negative values are rejected.
func ScanOptionsFromSettings(batchBlocks, catchUpBlocks, concurrency int) (ScanOptions, error) {
	if batchBlocks < 0 || catchUpBlocks < 0 || concurrency < 0 {
		return ScanOptions{}, fmt.Errorf("scan settings must not be negative (batch %d, catch-up %d, concurrency %d)", batchBlocks, catchUpBlocks, concurrency)
	}
	opts := DefaultScanOptions()
	if batchBlocks > 0 {
		opts.BatchBlocks = uint64(batchBlocks)
	}
	if catchUpBlocks > 0 {
		opts.CatchUpBlocks = uint64(catchUpBlocks)
	} else {
		opts.CatchUpBlocks = max(opts.CatchUpBlocks, opts.BatchBlocks)
	}
	if concurrency > 0 {
		opts.Concurrency = concurrency
	}
	if err := opts.Validate(); err != nil {
		return ScanOptions{}, err
	}
	return opts, nil
}

func (o ScanOptions) Validate() error {
	if o.BatchBlocks == 0 {
		return fmt.Errorf("scan batch must be at least one block")
	}
	if o.CatchUpBlocks < o.BatchBlocks {
		return fmt.Errorf("catch-up window (%d blocks) must not be smaller than the scan batch (%d blocks)", o.CatchUpBlocks, o.BatchBlocks)
	}
	if o.Concurrency < 1 || o.Concurrency > MaxScanConcurrency {
		return fmt.Errorf("scan concurrency must be between 1 and %d, got %d", MaxScanConcurrency, o.Concurrency)
	}
	return nil
}

// SetScanOptions replaces the scan window and concurrency; invalid options are rejected
// and the current ones kept.
func (s *Service) SetScanOptions(opts ScanOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	s.scan = opts
	return nil
}

// scanWindow is how many blocks after the checkpoint this cycle covers.
func (s *Service) scanWindow(lag uint64) uint64 {
	window := s.scan.BatchBlocks
	if lag > window {
		window = s.scan.CatchUpBlocks
	}
	return min(window, lag)
}

type fetchedBlock struct {
	number    uint64
	transfers []types.DetectedTransfer
	err       error
}

// scanRange records the deposits of blocks from..to and returns the last block the
// checkpoint may move to. A block whose deposits cannot be recorded is retried right
// away; if it keeps failing it is saved as pending and the scan moves on. The scan
// stops before a failing block that cannot be saved as pending, or when too many
// blocks failed this cycle, so the checkpoint never passes a block it could lose.
func (s *Service) scanRange(ctx context.Context, chainID string, adapter types.Chain, from, to uint64) (uint64, error) {
	if from == 0 {
		return 0, fmt.Errorf("scan range must start after block 0")
	}
	scanned := from - 1
	if from > to {
		return scanned, nil
	}
	newPending := 0
	for chunkStart := from; ; {
		chunkEnd := to
		if to-chunkStart >= uint64(s.scan.Concurrency) {
			chunkEnd = chunkStart + uint64(s.scan.Concurrency) - 1
		}
		for _, block := range fetchBlocks(ctx, adapter, chunkStart, chunkEnd) {
			outcome := s.processBlock(ctx, chainID, adapter, block, adapter.ScanBlock)
			if outcome.err != nil {
				if err := ctx.Err(); err != nil {
					return scanned, fmt.Errorf("scan block %d interrupted: %w", block.number, err)
				}
				if newPending >= s.failure.MaxNewPendingPerCycle {
					return scanned, fmt.Errorf("%d blocks already went pending this cycle, stopping before block %d: %w", newPending, block.number, outcome.err)
				}
				if err := s.recordPending(ctx, chainID, block.number, outcome); err != nil {
					return scanned, fmt.Errorf("block %d could not be recorded nor saved as pending, the checkpoint stays before it: %w", block.number, errors.Join(outcome.err, err))
				}
				newPending++
			}
			scanned = block.number
		}
		if chunkEnd == to {
			return scanned, nil
		}
		chunkStart = chunkEnd + 1
	}
}

// fetchBlocks loads blocks from..to concurrently and returns them in block order.
func fetchBlocks(ctx context.Context, adapter types.Chain, from, to uint64) []fetchedBlock {
	blocks := make([]fetchedBlock, to-from+1)
	var wg sync.WaitGroup
	for i := range blocks {
		wg.Add(1)
		go func(block *fetchedBlock, number uint64) {
			defer wg.Done()
			transfers, err := adapter.ScanBlock(ctx, number)
			*block = fetchedBlock{number: number, transfers: transfers, err: err}
		}(&blocks[i], from+uint64(i))
	}
	wg.Wait()
	return blocks
}

// blockFetcher loads the transfers of one block for a retry after a failed fetch.
type blockFetcher func(ctx context.Context, blockNum uint64) ([]types.DetectedTransfer, error)

// blockResult is the outcome of recording one block: how many deposits were new and,
// on failure, the transactions that failed and how many tries were made.
type blockResult struct {
	recorded int
	failedTx []string
	tries    int
	err      error
}

// processBlock records a fetched block, retrying right away with short backoff when the
// fetch or a deposit write fails. Recording is idempotent, so a retry only adds the
// deposits the failed try missed.
func (s *Service) processBlock(ctx context.Context, chainID string, adapter types.Chain, block fetchedBlock, fetch blockFetcher) blockResult {
	delays := s.failure.immediateDelays()
	for attempt := 0; ; attempt++ {
		outcome := s.tryBlock(ctx, chainID, adapter, block)
		outcome.tries = attempt + 1
		if outcome.err == nil || attempt == len(delays) || ctx.Err() != nil {
			return outcome
		}
		slog.Warn("deposit block failed, retrying",
			"chain", chainID, "block", block.number, "retry", attempt+1, "of", len(delays),
			"delay", delays[attempt].String(), "error_class", errorClass(outcome.err), "error", outcome.err)
		if err := s.sleep(ctx, delays[attempt]); err != nil {
			return outcome
		}
		if block.err != nil {
			block.transfers, block.err = fetch(ctx, block.number)
		}
	}
}

func (s *Service) tryBlock(ctx context.Context, chainID string, adapter types.Chain, block fetchedBlock) blockResult {
	if block.err != nil {
		return blockResult{err: classify(pending.ClassRPC, fmt.Errorf("scan block %d: %w", block.number, block.err))}
	}
	recorded, failedTx, err := s.recordTransfers(ctx, chainID, adapter, block.transfers)
	if err != nil {
		err = fmt.Errorf("record block %d: %w", block.number, err)
	}
	return blockResult{recorded: recorded, failedTx: failedTx, err: err}
}

// recordTransfers records each transfer to a watched address and returns how many
// deposits were new. Every transfer is tried even after one fails; the failed
// transactions and their joined error are returned.
func (s *Service) recordTransfers(ctx context.Context, chainID string, adapter types.Chain, transfers []types.DetectedTransfer) (int, []string, error) {
	recorded := 0
	var failedTx []string
	var failures []error
	for _, transfer := range transfers {
		created, err := s.processTransfer(ctx, chainID, adapter, transfer)
		if err != nil {
			failedTx = append(failedTx, transfer.TxHash)
			failures = append(failures, fmt.Errorf("tx %s: %w", transfer.TxHash, err))
			continue
		}
		if created {
			recorded++
		}
	}
	return recorded, failedTx, errors.Join(failures...)
}

// ScanBlock records the deposits of one block without touching the checkpoint, to
// repair a block the scanner missed or has not reached yet. Recording is idempotent
// (one deposit per chain and transaction), and the regular scan cycle moves the
// deposit through its confirmations and webhooks. A block that keeps failing is saved
// as pending; one that succeeds leaves the pending list.
func (s *Service) ScanBlock(ctx context.Context, chainID string, blockNum uint64) (int, error) {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return 0, err
	}
	if err := requireReachedBlock(ctx, adapter, blockNum); err != nil {
		return 0, err
	}
	transfers, fetchErr := adapter.ScanBlock(ctx, blockNum)
	outcome := s.processBlock(ctx, chainID, adapter, fetchedBlock{number: blockNum, transfers: transfers, err: fetchErr}, adapter.ScanBlock)
	if outcome.err != nil {
		return outcome.recorded, s.failTargetedScan(ctx, chainID, blockNum, outcome)
	}
	if err := s.resolvePending(ctx, chainID, blockNum); err != nil {
		slog.Warn("targeted block scan succeeded but its pending entry was not removed", "chain", chainID, "block", blockNum, "error", err)
	}
	slog.Info("targeted block scan complete", "chain", chainID, "block", blockNum, "transfers", len(transfers), "recorded", outcome.recorded)
	return outcome.recorded, nil
}

// ScanTransaction records the deposits of one finalized transaction, found through the
// block that holds it; the other transactions of that block are left to the scanner.
func (s *Service) ScanTransaction(ctx context.Context, chainID, txHash string) (int, error) {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return 0, fmt.Errorf("transaction hash is required")
	}
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return 0, err
	}
	blockNum, err := adapter.GetTransactionBlock(ctx, txHash)
	if err != nil {
		return 0, fmt.Errorf("locate transaction %s: %w", txHash, err)
	}
	if blockNum == 0 {
		return 0, fmt.Errorf("transaction %s is unknown or not final yet on %s", txHash, chainID)
	}
	if err := requireReachedBlock(ctx, adapter, blockNum); err != nil {
		return 0, err
	}
	fetchOwn := func(ctx context.Context, number uint64) ([]types.DetectedTransfer, error) {
		transfers, err := adapter.ScanBlock(ctx, number)
		if err != nil {
			return nil, err
		}
		return transfersOfTx(transfers, txHash), nil
	}
	own, fetchErr := fetchOwn(ctx, blockNum)
	outcome := s.processBlock(ctx, chainID, adapter, fetchedBlock{number: blockNum, transfers: own, err: fetchErr}, fetchOwn)
	if outcome.err != nil {
		if len(outcome.failedTx) == 0 {
			outcome.failedTx = []string{txHash}
		}
		return outcome.recorded, s.failTargetedScan(ctx, chainID, blockNum, outcome)
	}
	slog.Info("targeted transaction scan complete", "chain", chainID, "tx", txHash, "block", blockNum, "transfers", len(own), "recorded", outcome.recorded)
	return outcome.recorded, nil
}

// failTargetedScan saves the failed block as pending so the reprocessor keeps trying
// it, and reports the failure to the operator either way.
func (s *Service) failTargetedScan(ctx context.Context, chainID string, blockNum uint64, outcome blockResult) error {
	if err := s.recordPending(ctx, chainID, blockNum, outcome); err != nil {
		return fmt.Errorf("block %d failed and was not saved as pending: %w", blockNum, errors.Join(outcome.err, err))
	}
	return fmt.Errorf("block %d failed after %d tries and was saved as pending for retry: %w", blockNum, outcome.tries, outcome.err)
}

func transfersOfTx(transfers []types.DetectedTransfer, txHash string) []types.DetectedTransfer {
	var own []types.DetectedTransfer
	for _, transfer := range transfers {
		if sameTxHash(transfer.TxHash, txHash) {
			own = append(own, transfer)
		}
	}
	return own
}

// requireReachedBlock refuses blocks past the head the scanner works from, so a
// targeted scan never records a deposit from a block that is not final yet.
func requireReachedBlock(ctx context.Context, adapter types.Chain, blockNum uint64) error {
	if blockNum == 0 {
		return fmt.Errorf("block number must be positive")
	}
	head, err := adapter.GetLatestBlock(ctx)
	if err != nil {
		return fmt.Errorf("get latest block: %w", err)
	}
	if blockNum > head {
		return fmt.Errorf("block %d is past the chain head %d", blockNum, head)
	}
	return nil
}

// sameTxHash compares hex hashes case-insensitively; other encodings, such as Solana's
// base58 signatures, are case-sensitive.
func sameTxHash(a, b string) bool {
	if strings.HasPrefix(a, "0x") || strings.HasPrefix(a, "0X") {
		return strings.EqualFold(a, b)
	}
	return a == b
}
