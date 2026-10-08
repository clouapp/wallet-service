package refresh

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// UTXOStore replaces the unspent outputs of one wallet on one chain.
type UTXOStore interface {
	ReplaceForWallet(ctx context.Context, walletID uuid.UUID, chainID string, rows []models.WalletUTXO) error
}

type UTXOService struct {
	utxoRepo      UTXOStore
	syncStateRepo SyncStateStore
}

// UTXODeps is everything the UTXO refresh service needs. A nil field means that
// dependency is absent.
type UTXODeps struct {
	UTXOs      UTXOStore
	SyncStates SyncStateStore
}

// NewUTXOService wires the UTXO refresh service from UTXODeps.
func NewUTXOService(deps UTXODeps) *UTXOService {
	return &UTXOService{
		utxoRepo:      deps.UTXOs,
		syncStateRepo: deps.SyncStates,
	}
}

func (s *UTXOService) ReplaceWalletUTXOs(ctx context.Context, wallet *models.Wallet, rows []models.WalletUTXO) error {
	if err := s.utxoRepo.ReplaceForWallet(ctx, wallet.ID, wallet.Chain, rows); err != nil {
		_ = s.syncStateRepo.UpdateFailure(ctx, wallet.ID, wallet.Chain, string(RefreshScopeUtxos), err.Error())
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
	if err := s.syncStateRepo.Upsert(ctx, syncState); err != nil {
		return fmt.Errorf("utxo refresh: upsert sync state: %w", err)
	}

	return nil
}
