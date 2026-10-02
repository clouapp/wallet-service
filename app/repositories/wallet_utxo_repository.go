package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/types"
)

// WalletUTXORepository persists unspent outputs for a wallet.
type WalletUTXORepository struct {
	db.Base
}

// NewWalletUTXORepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWalletUTXORepository(query orm.Query) *WalletUTXORepository {
	return &WalletUTXORepository{Base: db.NewBase(query)}
}

// ReplaceForWallet replaces the UTXO rows for one wallet and chain.
func (r *WalletUTXORepository) ReplaceForWallet(ctx context.Context, walletID uuid.UUID, chainID string, rows []models.WalletUTXO) error {
	for i := range rows {
		if err := rows[i].ValidateAmounts(); err != nil {
			return err
		}
	}
	if err := r.Transaction(ctx, func(tx orm.Query) error {
		if _, err := tx.Where("wallet_id = ? AND chain_id = ?", walletID, chainID).ForceDelete(&models.WalletUTXO{}); err != nil {
			return err
		}
		for i := range rows {
			if err := tx.Create(&rows[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("replace wallet utxos: %w", err)
	}
	return nil
}

// ListSpendable returns unspent outputs for a wallet and chain, largest first.
func (r *WalletUTXORepository) ListSpendable(ctx context.Context, walletID uuid.UUID, chainID string) ([]models.WalletUTXO, error) {
	var utxos []models.WalletUTXO
	if err := r.Query(ctx).
		Where("wallet_id = ? AND chain_id = ? AND status = ?", walletID, chainID, string(types.UTXOStatusUnspent)).
		Order("value_raw DESC").
		Find(&utxos); err != nil {
		return nil, fmt.Errorf("list spendable utxos: %w", err)
	}
	return utxos, nil
}
