package refresh

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// BalanceChains is the registered adapter and token list a balance refresh reads.
type BalanceChains interface {
	Chain(id string) (types.Chain, error)
	TokensForChain(chainID string) []types.Token
}

// WalletSummaryStore writes the native balance columns a balance refresh updates.
type WalletSummaryStore interface {
	SetBalanceSummary(ctx context.Context, id uuid.UUID, asset, raw, display string, syncedAt time.Time, readModelStatus string) error
}

// AssetBalanceStore replaces the asset rows of one wallet on one chain.
type AssetBalanceStore interface {
	ReplaceForWallet(ctx context.Context, walletID uuid.UUID, chainID string, rows []models.WalletAssetBalance) error
}

// SnapshotStore records a balance snapshot and keeps the latest rows.
type SnapshotStore interface {
	Create(ctx context.Context, snapshot *models.WalletBalanceSnapshot) error
	TrimToLatest(ctx context.Context, walletID uuid.UUID, chainID string, keep int) error
}

// SyncStateStore records a successful refresh and a failed one.
type SyncStateStore interface {
	Upsert(ctx context.Context, state *models.WalletSyncState) error
	UpdateFailure(ctx context.Context, walletID uuid.UUID, chainID, scope, errMsg string) error
}

type BalanceService struct {
	registry         BalanceChains
	walletRepo       WalletSummaryStore
	assetBalanceRepo AssetBalanceStore
	snapshotRepo     SnapshotStore
	syncStateRepo    SyncStateStore
}

// Deps is everything the balance refresh service needs. A nil field means that
// dependency is absent.
type Deps struct {
	Registry      BalanceChains
	Wallets       WalletSummaryStore
	AssetBalances AssetBalanceStore
	Snapshots     SnapshotStore
	SyncStates    SyncStateStore
}

// NewBalanceService wires the balance refresh service from Deps.
func NewBalanceService(deps Deps) *BalanceService {
	return &BalanceService{
		registry:         deps.Registry,
		walletRepo:       deps.Wallets,
		assetBalanceRepo: deps.AssetBalances,
		snapshotRepo:     deps.Snapshots,
		syncStateRepo:    deps.SyncStates,
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
		s.recordFailure(ctx, wallet.ID, wallet.Chain, err)
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

	if err := s.assetBalanceRepo.ReplaceForWallet(ctx, wallet.ID, wallet.Chain, rows); err != nil {
		s.recordFailure(ctx, wallet.ID, wallet.Chain, err)
		return fmt.Errorf("replace asset balances: %w", err)
	}

	balanceAsset := nativeBal.Asset
	balanceRaw := nativeBal.Amount.String()
	balanceDisplay := nativeBal.Human
	if err := s.walletRepo.SetBalanceSummary(ctx, wallet.ID, balanceAsset, balanceRaw, balanceDisplay, now, string(types.ReadModelSynced)); err != nil {
		s.recordFailure(ctx, wallet.ID, wallet.Chain, err)
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
	if err := s.snapshotRepo.Create(ctx, snapshot); err != nil {
		s.recordFailure(ctx, wallet.ID, wallet.Chain, err)
		return fmt.Errorf("create balance snapshot: %w", err)
	}

	if err := s.snapshotRepo.TrimToLatest(ctx, wallet.ID, wallet.Chain, 10); err != nil {
		slog.Warn("trim snapshots failed", "wallet", wallet.ID, "error", err)
	}

	syncedAt := now
	if err := s.syncStateRepo.Upsert(ctx, &models.WalletSyncState{
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

func (s *BalanceService) recordFailure(ctx context.Context, walletID uuid.UUID, chainID string, cause error) {
	if err := s.syncStateRepo.UpdateFailure(ctx, walletID, chainID, string(RefreshScopeBalances), cause.Error()); err != nil {
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
