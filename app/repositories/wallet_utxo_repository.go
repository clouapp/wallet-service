package repositories

import (
	"github.com/google/uuid"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

type WalletUTXORepository interface {
	ReplaceForWallet(walletID uuid.UUID, chainID string, rows []models.WalletUTXO) error
	ListSpendable(walletID uuid.UUID, chainID string) ([]models.WalletUTXO, error)
}

type walletUTXORepository struct{}

func NewWalletUTXORepository() WalletUTXORepository {
	return &walletUTXORepository{}
}

func (r *walletUTXORepository) ReplaceForWallet(walletID uuid.UUID, chainID string, rows []models.WalletUTXO) error {
	return facades.Orm().Transaction(func(tx contractsorm.Query) error {
		_, err := tx.Where("wallet_id = ? AND chain_id = ?", walletID, chainID).
			ForceDelete(&models.WalletUTXO{})
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

func (r *walletUTXORepository) ListSpendable(walletID uuid.UUID, chainID string) ([]models.WalletUTXO, error) {
	var utxos []models.WalletUTXO
	err := facades.Orm().Query().
		Where("wallet_id = ? AND chain_id = ? AND status = ?", walletID, chainID, string(types.UTXOStatusUnspent)).
		Order("value_raw DESC").
		Find(&utxos)
	return utxos, err
}
