package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

const zeroBalanceRaw = "0"

// ChainRegistryRepository is the chain-registry store backed by the application database.
type ChainRegistryRepository struct {
	db.Base
}

// NewChainRegistryRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewChainRegistryRepository(query orm.Query) *ChainRegistryRepository {
	return &ChainRegistryRepository{Base: db.NewBase(query)}
}

// Chains returns every chain ordered by display_order.
func (r *ChainRegistryRepository) Chains(ctx context.Context) ([]models.Chain, error) {
	var chains []models.Chain
	if err := r.Query(ctx).Order("display_order ASC").Find(&chains); err != nil {
		return nil, fmt.Errorf("list chains: %w", err)
	}
	return chains, nil
}

// ChainHoldsBalance looks at the cached per-asset balances and the wallet-level
// native balance; both are written by the balance refresh.
func (r *ChainRegistryRepository) ChainHoldsBalance(ctx context.Context, chainID string) (bool, error) {
	assetRows, err := r.Query(ctx).Table("wallet_asset_balances").
		Where("chain_id = ?", chainID).
		Where("amount_raw <> ?", zeroBalanceRaw).
		Count()
	if err != nil {
		return false, fmt.Errorf("count asset balances: %w", err)
	}
	if assetRows > 0 {
		return true, nil
	}
	walletRows, err := r.Query(ctx).Table("wallets").
		Where("chain = ?", chainID).
		Where("COALESCE(balance_raw, ?) NOT IN (?, '')", zeroBalanceRaw, zeroBalanceRaw).
		Count()
	if err != nil {
		return false, fmt.Errorf("count wallet balances: %w", err)
	}
	return walletRows > 0, nil
}

// UpdateChainNetwork writes network_id and is_testnet. A missing chain is ErrRepositoryNotFound.
func (r *ChainRegistryRepository) UpdateChainNetwork(ctx context.Context, chainID string, networkID *int64, isTestnet bool) error {
	result, err := r.Query(ctx).Model(&models.Chain{}).
		Where("id = ?", chainID).
		Update(map[string]any{"network_id": networkID, "is_testnet": isTestnet})
	if err != nil {
		return fmt.Errorf("update chain network: %w", err)
	}
	return db.RequireRow(result)
}

// FindAccount returns the account, or ErrRepositoryNotFound.
func (r *ChainRegistryRepository) FindAccount(ctx context.Context, id uuid.UUID) (*models.Account, error) {
	var account models.Account
	if err := r.Query(ctx).Where("id = ?", id).First(&account); err != nil {
		return nil, fmt.Errorf("find account: %w", err)
	}
	if account.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &account, nil
}

// UpdateAccountEnvironment writes accounts.environment. A missing account is ErrRepositoryNotFound.
func (r *ChainRegistryRepository) UpdateAccountEnvironment(ctx context.Context, id uuid.UUID, environment string) error {
	result, err := r.Query(ctx).Model(&models.Account{}).
		Where("id = ?", id).
		Update("environment", environment)
	if err != nil {
		return fmt.Errorf("update account environment: %w", err)
	}
	return db.RequireRow(result)
}

// WalletsOnChain returns the wallets on a chain, oldest first.
func (r *ChainRegistryRepository) WalletsOnChain(ctx context.Context, chainID string) ([]models.Wallet, error) {
	var wallets []models.Wallet
	if err := r.Query(ctx).Where("chain = ?", chainID).Order("created_at ASC").Find(&wallets); err != nil {
		return nil, fmt.Errorf("list wallets on chain: %w", err)
	}
	return wallets, nil
}

// ActiveAddressesOfWallet returns the wallet's active addresses, by derivation index.
func (r *ChainRegistryRepository) ActiveAddressesOfWallet(ctx context.Context, walletID uuid.UUID) ([]models.Address, error) {
	var addresses []models.Address
	err := r.Query(ctx).
		Where("wallet_id = ?", walletID).
		Where("is_active = ?", true).
		Order("derivation_index ASC").
		Find(&addresses)
	if err != nil {
		return nil, fmt.Errorf("list active addresses: %w", err)
	}
	return addresses, nil
}

// ReissueGenesis retires the given addresses, inserts genesis, and points the wallet at it.
func (r *ChainRegistryRepository) ReissueGenesis(ctx context.Context, walletID uuid.UUID, genesis models.Address, retire []uuid.UUID) error {
	return r.Transaction(ctx, func(tx orm.Query) error {
		if len(retire) > 0 {
			if _, err := tx.Model(&models.Address{}).
				Where("wallet_id = ?", walletID).
				Where("id IN ?", retire).
				Update("is_active", false); err != nil {
				return fmt.Errorf("retire addresses: %w", err)
			}
		}
		if genesis.Address == "" {
			return nil
		}
		if genesis.WalletID != walletID {
			return fmt.Errorf("genesis address belongs to wallet %s, not %s", genesis.WalletID, walletID)
		}
		if err := tx.Create(&genesis); err != nil {
			return fmt.Errorf("create genesis address: %w", err)
		}
		result, err := tx.Model(&models.Wallet{}).
			Where("id = ?", walletID).
			Update("deposit_address_id", genesis.ID)
		if err != nil {
			return fmt.Errorf("link genesis address: %w", err)
		}
		return db.RequireRow(result)
	})
}
