package repositories

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WalletUserRepository interface {
	Create(wu *models.WalletUser) error
	FindByWalletID(walletID uuid.UUID) ([]models.WalletUser, error)
	FindByWalletAndUser(walletID, userID uuid.UUID) (*models.WalletUser, error)
	FindByWalletAndUserIncludeDeleted(walletID, userID uuid.UUID) (*models.WalletUser, error)
	UpdateField(id uuid.UUID, field string, value interface{}) error
	SoftDelete(walletID, userID uuid.UUID) error
}

type walletUserRepository struct{}

func NewWalletUserRepository() WalletUserRepository {
	return &walletUserRepository{}
}

func (r *walletUserRepository) Create(wu *models.WalletUser) error {
	return facades.Orm().Query().Create(wu)
}

func (r *walletUserRepository) FindByWalletID(walletID uuid.UUID) ([]models.WalletUser, error) {
	var members []models.WalletUser
	err := facades.Orm().Query().
		Where("wallet_id = ? AND deleted_at IS NULL", walletID).
		Find(&members)
	return members, err
}

func (r *walletUserRepository) FindByWalletAndUser(walletID, userID uuid.UUID) (*models.WalletUser, error) {
	var wu models.WalletUser
	err := facades.Orm().Query().
		Where("wallet_id = ? AND user_id = ? AND deleted_at IS NULL", walletID, userID).
		First(&wu)
	if err != nil {
		return nil, err
	}
	if wu.ID == uuid.Nil {
		return nil, nil
	}
	return &wu, nil
}

func (r *walletUserRepository) FindByWalletAndUserIncludeDeleted(walletID, userID uuid.UUID) (*models.WalletUser, error) {
	var wu models.WalletUser
	err := facades.Orm().Query().
		Where("wallet_id = ? AND user_id = ?", walletID, userID).
		First(&wu)
	if err != nil {
		return nil, err
	}
	if wu.ID == uuid.Nil {
		return nil, nil
	}
	return &wu, nil
}

func (r *walletUserRepository) UpdateField(id uuid.UUID, field string, value interface{}) error {
	_, err := facades.Orm().Query().
		Model(&models.WalletUser{}).
		Where("id = ?", id).
		Update(field, value)
	return err
}

func (r *walletUserRepository) SoftDelete(walletID, userID uuid.UUID) error {
	now := time.Now()
	_, err := facades.Orm().Query().
		Model(&models.WalletUser{}).
		Where("wallet_id = ? AND user_id = ? AND deleted_at IS NULL", walletID, userID).
		Update("deleted_at", now)
	return err
}
