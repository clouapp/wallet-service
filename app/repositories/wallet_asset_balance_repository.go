package repositories

import (
	"github.com/google/uuid"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WalletAssetBalanceRepository interface {
	ReplaceForWallet(walletID uuid.UUID, chainID string, rows []models.WalletAssetBalance) error
	ListByWallet(walletID uuid.UUID) ([]models.WalletAssetBalance, error)
}

type walletAssetBalanceRepository struct{}

func NewWalletAssetBalanceRepository() WalletAssetBalanceRepository {
	return &walletAssetBalanceRepository{}
}

func (r *walletAssetBalanceRepository) ReplaceForWallet(walletID uuid.UUID, chainID string, rows []models.WalletAssetBalance) error {
	return facades.Orm().Transaction(func(tx contractsorm.Query) error {
		_, err := tx.Where("wallet_id = ? AND chain_id = ?", walletID, chainID).
			ForceDelete(&models.WalletAssetBalance{})
		if err != nil {
			return err
		}
		for i := range rows {
			if err := tx.Create(&rows[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *walletAssetBalanceRepository) ListByWallet(walletID uuid.UUID) ([]models.WalletAssetBalance, error) {
	var balances []models.WalletAssetBalance
	err := facades.Orm().Query().
		Where("wallet_id = ?", walletID).
		Order("chain_id ASC, asset_symbol ASC").
		Find(&balances)
	return balances, err
}
