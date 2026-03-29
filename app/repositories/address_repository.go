package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type AddressRepository interface {
	Create(addr *models.Address) error
	CountByChainAndAddress(chainID, address string) (int64, error)
	FindByChainAndAddress(chainID, address string) (*models.Address, error)
	FindByExternalUserID(externalUserID string) ([]models.Address, error)
	FindByWalletID(walletID uuid.UUID) ([]models.Address, error)
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

func (r *addressRepository) FindByWalletID(walletID uuid.UUID) ([]models.Address, error) {
	var addrs []models.Address
	err := facades.Orm().Query().
		Where("wallet_id", walletID).
		Order("derivation_index").
		Find(&addrs)
	return addrs, err
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
