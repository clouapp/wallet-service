package deposit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

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
// and the current ones kept. The accepted window is also the fallback used when a
// later settings read fails.
func (s *Service) SetScanOptions(opts ScanOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	s.scan = opts
	s.scanFallback = opts
	return nil
}

// scanOptionSource reads the deposit_scan window at the moment of use.
// A non-nil error means the read failed and the service keeps the fallback.
type scanOptionSource func(ctx context.Context) (ScanOptions, error)

// SetScanOptionSource installs the per-scan reader. Nil keeps the window set
// at boot.
func (s *Service) SetScanOptionSource(source scanOptionSource) {
	if s == nil {
		return
	}
	s.scanSource = source
}

// resolveScanOptions applies a stored deposit_scan row for this invocation.
// A missing source, a read failure, or an invalid window keeps the fallback
// captured from the environment so a settings outage does not stop the scan.
func (s *Service) resolveScanOptions(ctx context.Context) {
	if s == nil || s.scanSource == nil {
		return
	}
	opts, err := s.scanSource(ctx)
	if err != nil {
		slog.Warn("deposit scan settings unread; keeping the environment window", "error", err)
		s.scan = s.scanFallback
		return
	}
	if err := opts.Validate(); err != nil {
		slog.Warn("deposit scan settings invalid; keeping the environment window", "error", err)
		s.scan = s.scanFallback
		return
	}
	s.scan = opts
}

// ApplyStoredScanOptions overlays a deposit_scan row on the environment window.
// A non-positive field is missing or invalid and leaves that part of base
// unchanged. A combination that fails Validate drops the fields that broke it.
func ApplyStoredScanOptions(base ScanOptions, batchBlocks, catchUpBlocks, concurrency int) ScanOptions {
	if base.Validate() != nil {
		base = DefaultScanOptions()
	}
	opts := overlayScanOptions(base, batchBlocks, catchUpBlocks, concurrency, true)
	if opts.Validate() == nil {
		return opts
	}
	opts = base
	if batchBlocks > 0 {
		candidate := overlayScanOptions(opts, batchBlocks, 0, 0, true)
		if candidate.Validate() == nil {
			opts = candidate
		}
	}
	if catchUpBlocks > 0 {
		candidate := overlayScanOptions(opts, 0, catchUpBlocks, 0, false)
		if candidate.Validate() == nil {
			opts = candidate
		}
	}
	if concurrency > 0 {
		candidate := overlayScanOptions(opts, 0, 0, concurrency, false)
		if candidate.Validate() == nil {
			opts = candidate
		}
	}
	return opts
}

func overlayScanOptions(base ScanOptions, batchBlocks, catchUpBlocks, concurrency int, raiseCatchUp bool) ScanOptions {
	opts := base
	if batchBlocks > 0 {
		opts.BatchBlocks = uint64(batchBlocks)
	}
	if catchUpBlocks > 0 {
		opts.CatchUpBlocks = uint64(catchUpBlocks)
	} else if raiseCatchUp && opts.CatchUpBlocks < opts.BatchBlocks {
		opts.CatchUpBlocks = opts.BatchBlocks
	}
	if concurrency > 0 {
		opts.Concurrency = concurrency
	}
	return opts
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

// scanRange records the deposits of blocks from..to and returns the last block of the
// unbroken run it fully recorded. A block that fails to load stops the range there, so
// the checkpoint never moves past a block whose deposits were not seen; the next cycle
// retries it. Rate-limited calls are already retried with backoff by the RPC client.
func (s *Service) scanRange(ctx context.Context, chainID string, adapter types.Chain, from, to uint64) (uint64, error) {
	if from == 0 {
		return 0, fmt.Errorf("scan range must start after block 0")
	}
	scanned := from - 1
	if from > to {
		return scanned, nil
	}
	for chunkStart := from; ; {
		chunkEnd := to
		if to-chunkStart >= uint64(s.scan.Concurrency) {
			chunkEnd = chunkStart + uint64(s.scan.Concurrency) - 1
		}
		for _, block := range fetchBlocks(ctx, adapter, chunkStart, chunkEnd) {
			if block.err != nil {
				return scanned, fmt.Errorf("scan block %d: %w", block.number, block.err)
			}
			s.recordTransfers(ctx, chainID, adapter, block.transfers)
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

// recordTransfers records each transfer to a watched address and returns how many
// deposits were new.
func (s *Service) recordTransfers(ctx context.Context, chainID string, adapter types.Chain, transfers []types.DetectedTransfer) int {
	recorded := 0
	for _, transfer := range transfers {
		created, err := s.processTransfer(ctx, chainID, adapter, transfer)
		if err != nil {
			slog.Error("process transfer", "tx", transfer.TxHash, "error", err)
			continue
		}
		if created {
			recorded++
		}
	}
	return recorded
}

// ScanBlock records the deposits of one block without touching the checkpoint, to
// repair a block the scanner missed or has not reached yet. Recording is idempotent
// (one deposit per chain and transaction), and the regular scan cycle moves the
// deposit through its confirmations and webhooks.
func (s *Service) ScanBlock(ctx context.Context, chainID string, blockNum uint64) (int, error) {
	adapter, err := s.registry.Chain(chainID)
	if err != nil {
		return 0, err
	}
	if err := requireReachedBlock(ctx, adapter, blockNum); err != nil {
		return 0, err
	}
	transfers, err := adapter.ScanBlock(ctx, blockNum)
	if err != nil {
		return 0, fmt.Errorf("scan block %d: %w", blockNum, err)
	}
	recorded := s.recordTransfers(ctx, chainID, adapter, transfers)
	slog.Info("targeted block scan complete", "chain", chainID, "block", blockNum, "transfers", len(transfers), "recorded", recorded)
	return recorded, nil
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
	transfers, err := adapter.ScanBlock(ctx, blockNum)
	if err != nil {
		return 0, fmt.Errorf("scan block %d: %w", blockNum, err)
	}
	var own []types.DetectedTransfer
	for _, transfer := range transfers {
		if sameTxHash(transfer.TxHash, txHash) {
			own = append(own, transfer)
		}
	}
	recorded := s.recordTransfers(ctx, chainID, adapter, own)
	slog.Info("targeted transaction scan complete", "chain", chainID, "tx", txHash, "block", blockNum, "transfers", len(own), "recorded", recorded)
	return recorded, nil
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
