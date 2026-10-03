package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/types"
)

// WalletSyncStateRepository persists read-model sync cursors.
type WalletSyncStateRepository struct {
	db.Base
}

// NewWalletSyncStateRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWalletSyncStateRepository(query orm.Query) *WalletSyncStateRepository {
	return &WalletSyncStateRepository{Base: db.NewBase(query)}
}

// Find returns the sync row, or ErrRepositoryNotFound.
func (r *WalletSyncStateRepository) Find(ctx context.Context, walletID uuid.UUID, chainID, scope string) (*models.WalletSyncState, error) {
	var state models.WalletSyncState
	if err := r.Query(ctx).Where("wallet_id = ? AND chain_id = ? AND sync_scope = ?", walletID, chainID, scope).First(&state); err != nil {
		return nil, fmt.Errorf("find wallet sync state: %w", err)
	}
	if state.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &state, nil
}

// Upsert inserts a sync row or updates its cursor columns. created_at of an existing row stays.
func (r *WalletSyncStateRepository) Upsert(ctx context.Context, state *models.WalletSyncState) error {
	if state == nil {
		return fmt.Errorf("upsert wallet sync state: state is nil")
	}
	var existing models.WalletSyncState
	if err := r.Query(ctx).Where("wallet_id = ? AND chain_id = ? AND sync_scope = ?", state.WalletID, state.ChainID, state.SyncScope).First(&existing); err != nil {
		return fmt.Errorf("upsert wallet sync state: %w", err)
	}
	if existing.ID == uuid.Nil {
		if err := r.Query(ctx).Create(state); err != nil {
			return fmt.Errorf("create wallet sync state: %w", err)
		}
		return nil
	}
	if _, err := r.Query(ctx).Model(&models.WalletSyncState{}).Where("id = ?", existing.ID).Update(map[string]any{
		"status":            state.Status,
		"cursor":            state.Cursor,
		"cursor_meta":       state.CursorMeta,
		"last_synced_at":    state.LastSyncedAt,
		"last_attempted_at": state.LastAttemptedAt,
		"last_error":        state.LastError,
		"next_reconcile_at": state.NextReconcileAt,
	}); err != nil {
		return fmt.Errorf("update wallet sync state: %w", err)
	}
	return nil
}

// UpdateFailure marks the scope failed, creating its row on a first sync that fails,
// so a wallet that never synced still shows why.
func (r *WalletSyncStateRepository) UpdateFailure(ctx context.Context, walletID uuid.UUID, chainID, scope, errMsg string) error {
	now := time.Now()
	result, err := r.Query(ctx).Model(&models.WalletSyncState{}).
		Where("wallet_id = ? AND chain_id = ? AND sync_scope = ?", walletID, chainID, scope).
		Update(map[string]any{
			"status":            string(types.SyncStatusFailed),
			"last_error":        errMsg,
			"last_attempted_at": now,
		})
	if err != nil {
		return fmt.Errorf("mark wallet sync failed: %w", err)
	}
	if result != nil && result.RowsAffected > 0 {
		return nil
	}
	if err := r.Query(ctx).Create(&models.WalletSyncState{
		ID:              uuid.New(),
		WalletID:        walletID,
		ChainID:         chainID,
		SyncScope:       scope,
		Status:          string(types.SyncStatusFailed),
		LastAttemptedAt: &now,
		LastError:       &errMsg,
	}); err != nil {
		return fmt.Errorf("create failed wallet sync state: %w", err)
	}
	return nil
}
