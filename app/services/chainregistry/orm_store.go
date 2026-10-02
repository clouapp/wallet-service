package chainregistry

import (
	"fmt"

	"github.com/google/uuid"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

const zeroBalanceRaw = "0"

// ORMStore is the Store backed by the application database.
type ORMStore struct{}

func NewORMStore() *ORMStore { return &ORMStore{} }

func (s *ORMStore) Chains() ([]models.Chain, error) {
	var chains []models.Chain
	err := facades.Orm().Query().Order("display_order ASC").Find(&chains)
	return chains, err
}

// ChainHoldsBalance looks at the cached per-asset balances and the wallet-level
// native balance; both are written by the balance refresh.
func (s *ORMStore) ChainHoldsBalance(chainID string) (bool, error) {
	assetRows, err := facades.Orm().Query().Table("wallet_asset_balances").
		Where("chain_id = ?", chainID).
		Where("amount_raw <> ?", zeroBalanceRaw).
		Count()
	if err != nil {
		return false, fmt.Errorf("count asset balances: %w", err)
	}
	if assetRows > 0 {
		return true, nil
	}
	walletRows, err := facades.Orm().Query().Table("wallets").
		Where("chain = ?", chainID).
		Where("COALESCE(balance_raw, ?) NOT IN (?, '')", zeroBalanceRaw, zeroBalanceRaw).
		Count()
	if err != nil {
		return false, fmt.Errorf("count wallet balances: %w", err)
	}
	return walletRows > 0, nil
}

func (s *ORMStore) UpdateChainNetwork(chainID string, networkID *int64, isTestnet bool) error {
	result, err := facades.Orm().Query().Model(&models.Chain{}).
		Where("id = ?", chainID).
		Update(map[string]any{"network_id": networkID, "is_testnet": isTestnet})
	if err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("chain %s not found", chainID)
	}
	return nil
}

func (s *ORMStore) FindAccount(id uuid.UUID) (*models.Account, error) {
	var account models.Account
	if err := facades.Orm().Query().Where("id = ?", id).First(&account); err != nil {
		return nil, err
	}
	if account.ID == uuid.Nil {
		return nil, nil
	}
	return &account, nil
}

func (s *ORMStore) UpdateAccountEnvironment(id uuid.UUID, environment string) error {
	result, err := facades.Orm().Query().Model(&models.Account{}).
		Where("id = ?", id).
		Update("environment", environment)
	if err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("account %s not found", id)
	}
	return nil
}

func (s *ORMStore) WalletsOnChain(chainID string) ([]models.Wallet, error) {
	var wallets []models.Wallet
	err := facades.Orm().Query().Where("chain = ?", chainID).Order("created_at ASC").Find(&wallets)
	return wallets, err
}

func (s *ORMStore) ActiveAddressesOfWallet(walletID uuid.UUID) ([]models.Address, error) {
	var addresses []models.Address
	err := facades.Orm().Query().
		Where("wallet_id = ?", walletID).
		Where("is_active = ?", true).
		Order("derivation_index ASC").
		Find(&addresses)
	return addresses, err
}

func (s *ORMStore) ReissueGenesis(walletID uuid.UUID, genesis models.Address, retire []uuid.UUID) error {
	return facades.Orm().Transaction(func(tx contractsorm.Query) error {
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
		if result.RowsAffected == 0 {
			return fmt.Errorf("wallet %s not found", walletID)
		}
		return nil
	})
}
