package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// AddressRepository persists deposit addresses.
type AddressRepository struct {
	db.Base
}

// NewAddressRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewAddressRepository(query orm.Query) *AddressRepository {
	return &AddressRepository{Base: db.NewBase(query)}
}

// Create inserts an address.
func (r *AddressRepository) Create(ctx context.Context, addr *models.Address) error {
	if addr == nil {
		return fmt.Errorf("create address: address is nil")
	}
	if err := r.Query(ctx).Create(addr); err != nil {
		return fmt.Errorf("create address: %w", err)
	}
	return nil
}

// SetLabel sets addresses.label.
func (r *AddressRepository) SetLabel(ctx context.Context, id uuid.UUID, label string) error {
	return r.updateColumn(ctx, id, "label", label, "set address label")
}

// SetExternalUserID sets addresses.external_user_id.
func (r *AddressRepository) SetExternalUserID(ctx context.Context, id uuid.UUID, externalUserID string) error {
	return r.updateColumn(ctx, id, "external_user_id", externalUserID, "set address external user")
}

// CountByChainAndAddress counts rows for one on-chain address on one chain.
func (r *AddressRepository) CountByChainAndAddress(ctx context.Context, chainID, address string) (int64, error) {
	count, err := r.Query(ctx).Model(&models.Address{}).Where("chain", chainID).Where("address", address).Count()
	if err != nil {
		return 0, fmt.Errorf("count addresses: %w", err)
	}
	return count, nil
}

// FindByChainAndAddress returns the address, or ErrRepositoryNotFound.
func (r *AddressRepository) FindByChainAndAddress(ctx context.Context, chainID, address string) (*models.Address, error) {
	var addr models.Address
	if err := r.Query(ctx).Where("chain", chainID).Where("address", address).First(&addr); err != nil {
		return nil, fmt.Errorf("find address: %w", err)
	}
	if addr.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &addr, nil
}

// FindByExternalUserID returns addresses for a customer-supplied external user id.
func (r *AddressRepository) FindByExternalUserID(ctx context.Context, externalUserID string) ([]models.Address, error) {
	var addrs []models.Address
	if err := r.Query(ctx).Where("external_user_id", externalUserID).Order("created_at").Find(&addrs); err != nil {
		return nil, fmt.Errorf("list addresses by external user: %w", err)
	}
	return addrs, nil
}

// FindByExternalUserIDAndAccount returns addresses whose wallet belongs to accountID.
func (r *AddressRepository) FindByExternalUserIDAndAccount(ctx context.Context, externalUserID string, accountID uuid.UUID) ([]models.Address, error) {
	var addrs []models.Address
	if err := r.Query(ctx).
		Where("external_user_id = ?", externalUserID).
		Where("wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)", accountID).
		Order("created_at").
		Find(&addrs); err != nil {
		return nil, fmt.Errorf("list addresses by external user and account: %w", err)
	}
	return addrs, nil
}

// FindByChainAndAddressAndAccount returns the address when its wallet belongs to
// accountID, or ErrRepositoryNotFound. A row owned by another account is the same miss.
func (r *AddressRepository) FindByChainAndAddressAndAccount(ctx context.Context, chainID, address string, accountID uuid.UUID) (*models.Address, error) {
	var addr models.Address
	if err := r.Query(ctx).
		Where("chain = ?", chainID).
		Where("address = ?", address).
		Where("wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)", accountID).
		First(&addr); err != nil {
		return nil, fmt.Errorf("find address for account: %w", err)
	}
	if addr.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &addr, nil
}

// FindByID returns the address, or ErrRepositoryNotFound.
func (r *AddressRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Address, error) {
	var addr models.Address
	if err := r.Query(ctx).Where("id = ?", id).First(&addr); err != nil {
		return nil, fmt.Errorf("find address by id: %w", err)
	}
	if addr.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &addr, nil
}

// FindByWalletID returns the wallet's addresses ordered by derivation index.
func (r *AddressRepository) FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.Address, error) {
	var addrs []models.Address
	if err := r.Query(ctx).Where("wallet_id", walletID).Order("derivation_index").Find(&addrs); err != nil {
		return nil, fmt.Errorf("list wallet addresses: %w", err)
	}
	return addrs, nil
}

// MaxDerivationIndex returns the highest derivation index, or -1 when the wallet has none.
func (r *AddressRepository) MaxDerivationIndex(ctx context.Context, walletID uuid.UUID) (int, error) {
	var maxIdx int
	if err := r.Query(ctx).Model(&models.Address{}).
		Where("wallet_id = ?", walletID).
		Select("COALESCE(MAX(derivation_index), -1)").
		Scan(&maxIdx); err != nil {
		return 0, fmt.Errorf("max derivation index: %w", err)
	}
	return maxIdx, nil
}

// PaginateByWalletID pages through a wallet's addresses ordered by derivation index.
func (r *AddressRepository) PaginateByWalletID(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error) {
	var addrs []models.Address
	total, err := r.Query(ctx).Model(&models.Address{}).Where("wallet_id", walletID).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count wallet addresses: %w", err)
	}
	if err := r.Query(ctx).Where("wallet_id", walletID).Order("derivation_index").Offset(offset).Limit(limit).Find(&addrs); err != nil {
		return nil, 0, fmt.Errorf("list wallet addresses: %w", err)
	}
	return addrs, total, nil
}

// PluckActiveAddresses returns the active on-chain address strings for a chain.
func (r *AddressRepository) PluckActiveAddresses(ctx context.Context, chainID string) ([]string, error) {
	var addresses []string
	if err := r.Query(ctx).Model(&models.Address{}).Where("chain", chainID).Where("is_active", true).Pluck("address", &addresses); err != nil {
		return nil, fmt.Errorf("pluck active addresses: %w", err)
	}
	return addresses, nil
}

func (r *AddressRepository) updateColumn(ctx context.Context, id uuid.UUID, column string, value any, op string) error {
	if _, err := r.Query(ctx).Model(&models.Address{}).Where("id = ?", id).Update(column, value); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
