package refresh

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/pkg/types"
)

type UTXOService struct {
	utxoRepo      repositories.WalletUTXORepository
	syncStateRepo repositories.WalletSyncStateRepository
}

func NewUTXOService(
	utxoRepo repositories.WalletUTXORepository,
	syncStateRepo repositories.WalletSyncStateRepository,
) *UTXOService {
	return &UTXOService{
		utxoRepo:      utxoRepo,
		syncStateRepo: syncStateRepo,
	}
}

func (s *UTXOService) ReplaceWalletUTXOs(_ context.Context, wallet *models.Wallet, rows []models.WalletUTXO) error {
	if err := s.utxoRepo.ReplaceForWallet(wallet.ID, wallet.Chain, rows); err != nil {
		_ = s.syncStateRepo.UpdateFailure(wallet.ID, wallet.Chain, string(RefreshScopeUtxos), err.Error())
		return fmt.Errorf("utxo refresh: replace utxos: %w", err)
	}

	now := time.Now().UTC()
	syncState := &models.WalletSyncState{
		ID:           uuid.New(),
		WalletID:     wallet.ID,
		ChainID:      wallet.Chain,
		SyncScope:    string(RefreshScopeUtxos),
		Status:       string(types.SyncStatusSynced),
		LastSyncedAt: &now,
	}
	if err := s.syncStateRepo.Upsert(syncState); err != nil {
		return fmt.Errorf("utxo refresh: upsert sync state: %w", err)
	}

	return nil
}
