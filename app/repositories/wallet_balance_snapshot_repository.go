package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// WalletBalanceSnapshotRepository persists balance history rows.
type WalletBalanceSnapshotRepository struct {
	db.Base
}

// NewWalletBalanceSnapshotRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWalletBalanceSnapshotRepository(query orm.Query) *WalletBalanceSnapshotRepository {
	return &WalletBalanceSnapshotRepository{Base: db.NewBase(query)}
}

// Create inserts a snapshot.
func (r *WalletBalanceSnapshotRepository) Create(ctx context.Context, snapshot *models.WalletBalanceSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("create balance snapshot: snapshot is nil")
	}
	if err := snapshot.ValidateAmounts(); err != nil {
		return err
	}
	if err := r.Query(ctx).Create(snapshot); err != nil {
		return fmt.Errorf("create balance snapshot: %w", err)
	}
	return nil
}

// TrimToLatest keeps the newest snapshots for a wallet and chain and deletes the rest.
func (r *WalletBalanceSnapshotRepository) TrimToLatest(ctx context.Context, walletID uuid.UUID, chainID string, keep int) error {
	var toKeep []models.WalletBalanceSnapshot
	if err := r.Query(ctx).Select("id").Where("wallet_id = ? AND chain_id = ?", walletID, chainID).Order("captured_at DESC").Limit(keep).Find(&toKeep); err != nil {
		return fmt.Errorf("list snapshots to keep: %w", err)
	}
	if len(toKeep) == 0 {
		return nil
	}
	keepIDs := make([]uuid.UUID, len(toKeep))
	for i, snapshot := range toKeep {
		keepIDs[i] = snapshot.ID
	}
	if _, err := r.Query(ctx).Where("wallet_id = ? AND chain_id = ?", walletID, chainID).Where("id NOT IN ?", keepIDs).ForceDelete(&models.WalletBalanceSnapshot{}); err != nil {
		return fmt.Errorf("trim balance snapshots: %w", err)
	}
	return nil
}

// ListRecent returns the newest snapshots for a wallet and chain.
func (r *WalletBalanceSnapshotRepository) ListRecent(ctx context.Context, walletID uuid.UUID, chainID string, limit int) ([]models.WalletBalanceSnapshot, error) {
	var snapshots []models.WalletBalanceSnapshot
	if err := r.Query(ctx).Where("wallet_id = ? AND chain_id = ?", walletID, chainID).Order("captured_at DESC").Limit(limit).Find(&snapshots); err != nil {
		return nil, fmt.Errorf("list balance snapshots: %w", err)
	}
	return snapshots, nil
}
