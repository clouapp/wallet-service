package repositories

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// DepositScanCheckpointRepository persists the block each chain's deposit scan
// resumes after. The checkpoint only moves forward.
type DepositScanCheckpointRepository struct {
	db.Base
}

// NewDepositScanCheckpointRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewDepositScanCheckpointRepository(query orm.Query) *DepositScanCheckpointRepository {
	return &DepositScanCheckpointRepository{Base: db.NewBase(query)}
}

// Find returns the chain's checkpoint, or ErrRepositoryNotFound for a chain never scanned.
func (r *DepositScanCheckpointRepository) Find(ctx context.Context, chainID string) (uint64, error) {
	var row models.DepositScanCheckpoint
	if err := r.Query(ctx).Where("chain", chainID).FirstOrFail(&row); err != nil {
		return 0, db.LookupError(err, "find deposit scan checkpoint")
	}
	return storedCheckpoint(row)
}

// Advance moves the chain's checkpoint to block, creating it on the first call, and
// returns the stored checkpoint; a block behind the stored one leaves it, so a slower
// scanner never rewinds the chain.
func (r *DepositScanCheckpointRepository) Advance(ctx context.Context, chainID string, block uint64) (uint64, error) {
	if strings.TrimSpace(chainID) == "" {
		return 0, fmt.Errorf("advance deposit scan checkpoint: chain is required")
	}
	if block > math.MaxInt64 {
		return 0, fmt.Errorf("advance deposit scan checkpoint: block %d does not fit a bigint", block)
	}
	var rows []models.DepositScanCheckpoint
	if err := r.Query(ctx).Raw(
		`INSERT INTO deposit_scan_checkpoints (chain, block_number, created_at, updated_at)
		 VALUES (?, ?, NOW(), NOW())
		 ON CONFLICT (chain) DO UPDATE
		 SET block_number = GREATEST(deposit_scan_checkpoints.block_number, EXCLUDED.block_number),
		     updated_at = NOW()
		 RETURNING chain, block_number, created_at, updated_at`,
		chainID, int64(block),
	).Scan(&rows); err != nil {
		return 0, fmt.Errorf("advance deposit scan checkpoint: %w", err)
	}
	if len(rows) == 0 {
		return 0, models.ErrRepositoryNotFound
	}
	return storedCheckpoint(rows[0])
}

// storedCheckpoint returns the row's block, or an error for a negative one.
func storedCheckpoint(row models.DepositScanCheckpoint) (uint64, error) {
	if row.BlockNumber < 0 {
		return 0, fmt.Errorf("deposit scan checkpoint of %s holds a negative block %d", row.Chain, row.BlockNumber)
	}
	return uint64(row.BlockNumber), nil
}
