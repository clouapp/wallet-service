package refresh

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

type BalanceService struct {
	registry         *chain.Registry
	walletRepo       repositories.WalletRepository
	assetBalanceRepo repositories.WalletAssetBalanceRepository
	snapshotRepo     repositories.WalletBalanceSnapshotRepository
	syncStateRepo    repositories.WalletSyncStateRepository
}

func NewBalanceService(
	registry *chain.Registry,
	walletRepo repositories.WalletRepository,
	assetBalanceRepo repositories.WalletAssetBalanceRepository,
	snapshotRepo repositories.WalletBalanceSnapshotRepository,
	syncStateRepo repositories.WalletSyncStateRepository,
) *BalanceService {
	return &BalanceService{
		registry:         registry,
		walletRepo:       walletRepo,
		assetBalanceRepo: assetBalanceRepo,
		snapshotRepo:     snapshotRepo,
		syncStateRepo:    syncStateRepo,
	}
}

func (s *BalanceService) RefreshWallet(ctx context.Context, wallet *models.Wallet) error {
	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil {
		return fmt.Errorf("chain adapter not found for %q: %w", wallet.Chain, err)
	}

	if wallet.DepositAddress == nil {
		return fmt.Errorf("wallet %s has no deposit address", wallet.ID)
	}
	address := wallet.DepositAddress.Address

	nativeBal, err := adapter.GetBalance(ctx, address)
	if err != nil {
		s.recordFailure(wallet.ID, wallet.Chain, err)
		return fmt.Errorf("get native balance for %s: %w", address, err)
	}

	now := time.Now()
	rows := []models.WalletAssetBalance{
		buildAssetBalanceRow(wallet.ID, wallet.Chain, string(types.AssetTypeNative), nativeBal.Asset, nativeBal, address, now),
	}

	tokens := s.registry.TokensForChain(wallet.Chain)
	for _, tok := range tokens {
		tokenBal, tokenErr := adapter.GetTokenBalance(ctx, address, tok)
		if tokenErr != nil {
			slog.Warn("skipping token balance",
				"wallet", wallet.ID,
				"chain", wallet.Chain,
				"token", tok.Symbol,
				"error", tokenErr,
			)
			continue
		}
		assetKey := tok.ChainID + ":" + tok.Symbol
		row := buildAssetBalanceRow(wallet.ID, wallet.Chain, string(types.AssetTypeToken), assetKey, tokenBal, address, now)
		row.AssetName = &tok.Name
		row.AssetContract = &tok.Contract
		rows = append(rows, row)
	}

	if err := s.assetBalanceRepo.ReplaceForWallet(wallet.ID, wallet.Chain, rows); err != nil {
		s.recordFailure(wallet.ID, wallet.Chain, err)
		return fmt.Errorf("replace asset balances: %w", err)
	}

	balanceAsset := nativeBal.Asset
	balanceRaw := nativeBal.Amount.String()
	balanceDisplay := nativeBal.Human
	if err := s.walletRepo.UpdateFields(wallet.ID, map[string]interface{}{
		"balance_asset":          balanceAsset,
		"balance_raw":            balanceRaw,
		"balance_display":        balanceDisplay,
		"balance_last_synced_at": now,
		"read_model_status":      string(types.ReadModelSynced),
	}); err != nil {
		s.recordFailure(wallet.ID, wallet.Chain, err)
		return fmt.Errorf("update wallet summary: %w", err)
	}

	snapshot := &models.WalletBalanceSnapshot{
		ID:             uuid.New(),
		WalletID:       wallet.ID,
		ChainID:        wallet.Chain,
		BalanceAsset:   nativeBal.Asset,
		BalanceRaw:     balanceRaw,
		BalanceDisplay: balanceDisplay,
		CapturedAt:     now,
	}
	if err := s.snapshotRepo.Create(snapshot); err != nil {
		s.recordFailure(wallet.ID, wallet.Chain, err)
		return fmt.Errorf("create balance snapshot: %w", err)
	}

	if err := s.snapshotRepo.TrimToLatest(wallet.ID, wallet.Chain, 10); err != nil {
		slog.Warn("trim snapshots failed", "wallet", wallet.ID, "error", err)
	}

	syncedAt := now
	if err := s.syncStateRepo.Upsert(&models.WalletSyncState{
		ID:           uuid.New(),
		WalletID:     wallet.ID,
		ChainID:      wallet.Chain,
		SyncScope:    string(RefreshScopeBalances),
		Status:       string(types.SyncStatusSynced),
		LastSyncedAt: &syncedAt,
	}); err != nil {
		slog.Warn("upsert sync state failed", "wallet", wallet.ID, "error", err)
	}

	return nil
}

func (s *BalanceService) recordFailure(walletID uuid.UUID, chainID string, cause error) {
	if err := s.syncStateRepo.UpdateFailure(walletID, chainID, string(RefreshScopeBalances), cause.Error()); err != nil {
		slog.Error("failed to record sync failure",
			"wallet", walletID,
			"chain", chainID,
			"cause", cause,
			"error", err,
		)
	}
}

func buildAssetBalanceRow(
	walletID uuid.UUID,
	chainID, assetType, assetKey string,
	bal *types.Balance,
	sourceAddress string,
	now time.Time,
) models.WalletAssetBalance {
	return models.WalletAssetBalance{
		ID:            uuid.New(),
		WalletID:      walletID,
		ChainID:       chainID,
		AssetType:     assetType,
		AssetSymbol:   bal.Asset,
		AssetKey:      assetKey,
		Decimals:      int(bal.Decimals),
		AmountRaw:     bal.Amount.String(),
		AmountDisplay: bal.Human,
		SourceAddress: &sourceAddress,
		LastSyncedAt:  now,
	}
}
