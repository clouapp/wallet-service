package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WalletRepository interface {
	Create(wallet *models.Wallet) error
	FindByID(id uuid.UUID) (*models.Wallet, error)
	FindByIDAndAccount(id, accountID uuid.UUID) (*models.Wallet, error)
	FindAll() ([]models.Wallet, error)
	PaginateAll(limit, offset int) ([]models.Wallet, int64, error)
	PaginateByAccount(accountID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error)
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
	err := facades.Orm().Query().With("DepositAddress").Where("id = ?", id).First(&wallet)
	if err != nil {
		return nil, err
	}
	if wallet.ID == uuid.Nil {
		return nil, nil
	}
	return &wallet, nil
}

func (r *walletRepository) FindByIDAndAccount(id, accountID uuid.UUID) (*models.Wallet, error) {
	var wallet models.Wallet
	err := facades.Orm().Query().With("DepositAddress").Where("id = ? AND account_id = ?", id, accountID).First(&wallet)
	if err != nil {
		return nil, err
	}
	if wallet.ID == uuid.Nil {
		return nil, nil
	}
	return &wallet, nil
}

func (r *walletRepository) PaginateByAccount(accountID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error) {
	var wallets []models.Wallet
	var total int64

	q := facades.Orm().Query().Model(&models.Wallet{}).Where("account_id = ?", accountID)
	if chain != "" {
		q = q.Where("chain = ?", chain)
	}

	total, err := q.Count()
	if err != nil {
		return nil, 0, err
	}

	q2 := facades.Orm().Query().With("DepositAddress").Where("account_id = ?", accountID)
	if chain != "" {
		q2 = q2.Where("chain = ?", chain)
	}
	err = q2.Order("created_at").Offset(offset).Limit(limit).Find(&wallets)

	return wallets, total, err
}

func (r *walletRepository) FindAll() ([]models.Wallet, error) {
	var wallets []models.Wallet
	err := facades.Orm().Query().With("DepositAddress").Order("created_at").Find(&wallets)
	return wallets, err
}

func (r *walletRepository) PaginateAll(limit, offset int) ([]models.Wallet, int64, error) {
	var wallets []models.Wallet
	var total int64
	total, err := facades.Orm().Query().
		Model(&models.Wallet{}).
		Count()
	if err != nil {
		return nil, 0, err
	}
	err = facades.Orm().Query().With("DepositAddress").
		Order("created_at").
		Offset(offset).Limit(limit).
		Find(&wallets)
	return wallets, total, err
}


func (r *walletRepository) UpdateField(id uuid.UUID, field string, value interface{}) error {
	_, err := facades.Orm().Query().Model(&models.Wallet{}).Where("id = ?", id).Update(field, value)
	return err
}

func (r *walletRepository) UpdateFields(id uuid.UUID, fields map[string]interface{}) error {
	_, err := facades.Orm().Query().Model(&models.Wallet{}).Where("id = ?", id).Update(fields)
	return err
}
