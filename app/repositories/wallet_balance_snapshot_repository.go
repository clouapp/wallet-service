package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WalletBalanceSnapshotRepository interface {
	Create(snapshot *models.WalletBalanceSnapshot) error
	TrimToLatest(walletID uuid.UUID, chainID string, keep int) error
	ListRecent(walletID uuid.UUID, chainID string, limit int) ([]models.WalletBalanceSnapshot, error)
}

type walletBalanceSnapshotRepository struct{}

func NewWalletBalanceSnapshotRepository() WalletBalanceSnapshotRepository {
	return &walletBalanceSnapshotRepository{}
}

func (r *walletBalanceSnapshotRepository) Create(snapshot *models.WalletBalanceSnapshot) error {
	return facades.Orm().Query().Create(snapshot)
}

func (r *walletBalanceSnapshotRepository) TrimToLatest(walletID uuid.UUID, chainID string, keep int) error {
	var toKeep []models.WalletBalanceSnapshot
	if err := facades.Orm().Query().
		Select("id").
		Where("wallet_id = ? AND chain_id = ?", walletID, chainID).
		Order("captured_at DESC").
		Limit(keep).
		Find(&toKeep); err != nil {
		return err
	}
	if len(toKeep) == 0 {
		return nil
	}
	keepIDs := make([]uuid.UUID, len(toKeep))
	for i, s := range toKeep {
		keepIDs[i] = s.ID
	}
	_, err := facades.Orm().Query().
		Where("wallet_id = ? AND chain_id = ?", walletID, chainID).
		Where("id NOT IN ?", keepIDs).
		ForceDelete(&models.WalletBalanceSnapshot{})
	return err
}

func (r *walletBalanceSnapshotRepository) ListRecent(walletID uuid.UUID, chainID string, limit int) ([]models.WalletBalanceSnapshot, error) {
	var snapshots []models.WalletBalanceSnapshot
	err := facades.Orm().Query().
		Where("wallet_id = ? AND chain_id = ?", walletID, chainID).
		Order("captured_at DESC").
		Limit(limit).
		Find(&snapshots)
	return snapshots, err
}
