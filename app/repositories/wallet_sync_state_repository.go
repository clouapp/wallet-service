package repositories

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

type WalletSyncStateRepository interface {
	Find(walletID uuid.UUID, chainID string, scope string) (*models.WalletSyncState, error)
	Upsert(state *models.WalletSyncState) error
	UpdateFailure(walletID uuid.UUID, chainID, scope, errMsg string) error
}

type walletSyncStateRepository struct{}

func NewWalletSyncStateRepository() WalletSyncStateRepository {
	return &walletSyncStateRepository{}
}

func (r *walletSyncStateRepository) Find(walletID uuid.UUID, chainID string, scope string) (*models.WalletSyncState, error) {
	var state models.WalletSyncState
	err := facades.Orm().Query().
		Where("wallet_id = ? AND chain_id = ? AND sync_scope = ?", walletID, chainID, scope).
		First(&state)
	if err != nil {
		return nil, err
	}
	if state.ID == uuid.Nil {
		return nil, nil
	}
	return &state, nil
}

func (r *walletSyncStateRepository) Upsert(state *models.WalletSyncState) error {
	var existing models.WalletSyncState
	err := facades.Orm().Query().
		Where("wallet_id = ? AND chain_id = ? AND sync_scope = ?", state.WalletID, state.ChainID, state.SyncScope).
		First(&existing)
	if err != nil {
		return err
	}
	if existing.ID == uuid.Nil {
		return facades.Orm().Query().Create(state)
	}
	state.ID = existing.ID
	state.CreatedAt = existing.CreatedAt
	return facades.Orm().Query().Save(state)
}

// UpdateFailure marks the scope failed, creating its row on a first sync that fails,
// so a wallet that never synced still shows why.
func (r *walletSyncStateRepository) UpdateFailure(walletID uuid.UUID, chainID, scope, errMsg string) error {
	now := time.Now()
	result, err := facades.Orm().Query().
		Model(&models.WalletSyncState{}).
		Where("wallet_id = ? AND chain_id = ? AND sync_scope = ?", walletID, chainID, scope).
		Update(map[string]any{
			"status":            string(types.SyncStatusFailed),
			"last_error":        errMsg,
			"last_attempted_at": now,
		})
	if err != nil {
		return err
	}
	if result != nil && result.RowsAffected > 0 {
		return nil
	}
	return facades.Orm().Query().Create(&models.WalletSyncState{
		ID:              uuid.New(),
		WalletID:        walletID,
		ChainID:         chainID,
		SyncScope:       scope,
		Status:          string(types.SyncStatusFailed),
		LastAttemptedAt: &now,
		LastError:       &errMsg,
	})
}
