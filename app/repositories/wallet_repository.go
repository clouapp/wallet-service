package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WalletRepository interface {
	Create(wallet *models.Wallet) error
	FindByID(id uuid.UUID) (*models.Wallet, error)
	FindAll() ([]models.Wallet, error)
	CountByChain(chainID string) (int64, error)
	UpdateField(id uuid.UUID, field string, value interface{}) error
	UpdateFields(id uuid.UUID, fields map[string]interface{}) error
}

type walletRepository struct{}

func NewWalletRepository() WalletRepository {
	return &walletRepository{}
}

func (r *walletRepository) Create(wallet *models.Wallet) error {
	return facades.Orm().Query().Create(wallet)
}

func (r *walletRepository) FindByID(id uuid.UUID) (*models.Wallet, error) {
	var wallet models.Wallet
	err := facades.Orm().Query().Where("id = ?", id).First(&wallet)
	if err != nil {
		return nil, err
	}
	if wallet.ID == uuid.Nil {
		return nil, nil
	}
	return &wallet, nil
}

func (r *walletRepository) FindAll() ([]models.Wallet, error) {
	var wallets []models.Wallet
	err := facades.Orm().Query().Order("created_at").Find(&wallets)
	return wallets, err
}

func (r *walletRepository) CountByChain(chainID string) (int64, error) {
	return facades.Orm().Query().Model(&models.Wallet{}).Where("chain", chainID).Count()
}

func (r *walletRepository) UpdateField(id uuid.UUID, field string, value interface{}) error {
	_, err := facades.Orm().Query().Model(&models.Wallet{}).Where("id = ?", id).Update(field, value)
	return err
}

func (r *walletRepository) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	_, err := facades.Orm().Query().Model(&models.Wallet{}).Where("id = ?", id).Update(fields)
	return err
}
