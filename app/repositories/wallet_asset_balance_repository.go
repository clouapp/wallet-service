package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// WalletAssetBalanceRepository persists per-asset balances for a wallet.
type WalletAssetBalanceRepository struct {
	db.Base
}

// NewWalletAssetBalanceRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWalletAssetBalanceRepository(query orm.Query) *WalletAssetBalanceRepository {
	return &WalletAssetBalanceRepository{Base: db.NewBase(query)}
}

// ReplaceForWallet replaces the asset rows for one wallet and chain.
func (r *WalletAssetBalanceRepository) ReplaceForWallet(ctx context.Context, walletID uuid.UUID, chainID string, rows []models.WalletAssetBalance) error {
	for i := range rows {
		if err := rows[i].ValidateAmounts(); err != nil {
			return err
		}
	}
	if err := r.Transaction(ctx, func(tx orm.Query) error {
		if _, err := tx.Where("wallet_id = ? AND chain_id = ?", walletID, chainID).ForceDelete(&models.WalletAssetBalance{}); err != nil {
			return err
		}
		for i := range rows {
			if err := tx.Create(&rows[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("replace wallet asset balances: %w", err)
	}
	return nil
}

// ListByWallet returns the asset rows for one wallet.
func (r *WalletAssetBalanceRepository) ListByWallet(ctx context.Context, walletID uuid.UUID) ([]models.WalletAssetBalance, error) {
	var balances []models.WalletAssetBalance
	if err := r.Query(ctx).Where("wallet_id = ?", walletID).Order("chain_id ASC, asset_symbol ASC").Find(&balances); err != nil {
		return nil, fmt.Errorf("list wallet asset balances: %w", err)
	}
	return balances, nil
}

// ListByWallets returns the asset rows for the given wallets. An empty id list is an empty result.
func (r *WalletAssetBalanceRepository) ListByWallets(ctx context.Context, walletIDs []uuid.UUID) ([]models.WalletAssetBalance, error) {
	if len(walletIDs) == 0 {
		return []models.WalletAssetBalance{}, nil
	}
	var balances []models.WalletAssetBalance
	if err := r.Query(ctx).Where("wallet_id IN ?", walletIDs).Order("chain_id ASC, asset_type ASC, asset_symbol ASC").Find(&balances); err != nil {
		return nil, fmt.Errorf("list asset balances: %w", err)
	}
	return balances, nil
}
