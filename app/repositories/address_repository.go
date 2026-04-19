package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type AddressRepository interface {
	Create(addr *models.Address) error
	UpdateFields(id uuid.UUID, fields map[string]interface{}) error
	CountByChainAndAddress(chainID, address string) (int64, error)
	FindByChainAndAddress(chainID, address string) (*models.Address, error)
	FindByChainAndAddressAndAccount(chainID, address string, accountID uuid.UUID) (*models.Address, error)
	FindByExternalUserID(externalUserID string) ([]models.Address, error)
	FindByExternalUserIDAndAccount(externalUserID string, accountID uuid.UUID) ([]models.Address, error)
	FindByID(id uuid.UUID) (*models.Address, error)
	FindByWalletID(walletID uuid.UUID) ([]models.Address, error)
	MaxDerivationIndex(walletID uuid.UUID) (int, error)
	PaginateByWalletID(walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error)
	PluckActiveAddresses(chainID string) ([]string, error)
}

type addressRepository struct{}

func NewAddressRepository() AddressRepository {
	return &addressRepository{}
}

func (r *addressRepository) Create(addr *models.Address) error {
	return facades.Orm().Query().Create(addr)
}

func (r *addressRepository) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	_, err := facades.Orm().Query().
		Model(&models.Address{}).
		Where("id = ?", id).
		Update(fields)
	return err
}

func (r *addressRepository) CountByChainAndAddress(chainID, address string) (int64, error) {
	return facades.Orm().Query().
		Model(&models.Address{}).
		Where("chain", chainID).
		Where("address", address).
		Count()
}

func (r *addressRepository) FindByChainAndAddress(chainID, address string) (*models.Address, error) {
	var addr models.Address
	err := facades.Orm().Query().
		Where("chain", chainID).
		Where("address", address).
		First(&addr)
	if err != nil {
		return nil, err
	}
	if addr.ID == uuid.Nil {
		return nil, nil
	}
	return &addr, nil
}

func (r *addressRepository) FindByExternalUserID(externalUserID string) ([]models.Address, error) {
	var addrs []models.Address
	err := facades.Orm().Query().
		Where("external_user_id", externalUserID).
		Order("created_at").
		Find(&addrs)
	return addrs, err
}

// FindByExternalUserIDAndAccount returns addresses matching externalUserID whose
// wallet belongs to accountID. This is the account-scoped variant used by the
// external API to prevent IDOR across accounts that happen to share the same
// customer-supplied external_user_id.
func (r *addressRepository) FindByExternalUserIDAndAccount(externalUserID string, accountID uuid.UUID) ([]models.Address, error) {
	var addrs []models.Address
	err := facades.Orm().Query().
		Where("external_user_id = ?", externalUserID).
		Where("wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)", accountID).
		Order("created_at").
		Find(&addrs)
	return addrs, err
}

// FindByChainAndAddressAndAccount returns the address iff its wallet belongs to
// accountID. Returns (nil, nil) when the row doesn't exist OR belongs to a
// different account — callers must not distinguish the two cases (IDOR mitigation).
func (r *addressRepository) FindByChainAndAddressAndAccount(chainID, address string, accountID uuid.UUID) (*models.Address, error) {
	var addr models.Address
	err := facades.Orm().Query().
		Where("chain = ?", chainID).
		Where("address = ?", address).
		Where("wallet_id IN (SELECT id FROM wallets WHERE account_id = ?)", accountID).
		First(&addr)
	if err != nil {
		return nil, err
	}
	if addr.ID == uuid.Nil {
		return nil, nil
	}
	return &addr, nil
}

func (r *addressRepository) FindByID(id uuid.UUID) (*models.Address, error) {
	var addr models.Address
	err := facades.Orm().Query().Where("id = ?", id).First(&addr)
	if err != nil {
		return nil, err
	}
	if addr.ID == uuid.Nil {
		return nil, nil
	}
	return &addr, nil
}

func (r *addressRepository) FindByWalletID(walletID uuid.UUID) ([]models.Address, error) {
	var addrs []models.Address
	err := facades.Orm().Query().
		Where("wallet_id", walletID).
		Order("derivation_index").
		Find(&addrs)
	return addrs, err
}

func (r *addressRepository) MaxDerivationIndex(walletID uuid.UUID) (int, error) {
	var maxIdx int
	err := facades.Orm().Query().
		Model(&models.Address{}).
		Where("wallet_id = ?", walletID).
		Select("COALESCE(MAX(derivation_index), -1)").
		Scan(&maxIdx)
	return maxIdx, err
}

func (r *addressRepository) PaginateByWalletID(walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error) {
	var addrs []models.Address
	var total int64
	total, err := facades.Orm().Query().
		Model(&models.Address{}).
		Where("wallet_id", walletID).
		Count()
	if err != nil {
		return nil, 0, err
	}
	err = facades.Orm().Query().
		Where("wallet_id", walletID).
		Order("derivation_index").
		Offset(offset).Limit(limit).
		Find(&addrs)
	return addrs, total, err
}

func (r *addressRepository) PluckActiveAddresses(chainID string) ([]string, error) {
	var addresses []string
	err := facades.Orm().Query().
		Model(&models.Address{}).
		Where("chain", chainID).
		Where("is_active", true).
		Pluck("address", &addresses)
	return addresses, err
}
