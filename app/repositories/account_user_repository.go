package repositories

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type AccountUserRepository interface {
	Create(au *models.AccountUser) error
	FindByAccountID(accountID uuid.UUID) ([]models.AccountUser, error)
	FindByAccountAndUser(accountID, userID uuid.UUID) (*models.AccountUser, error)
	FindByAccountAndUserIncludeDeleted(accountID, userID uuid.UUID) (*models.AccountUser, error)
	FindByUserID(userID uuid.UUID) ([]models.AccountUser, error)
	UpdateField(id uuid.UUID, field string, value interface{}) error
	SoftDeleteByAccountAndUser(accountID, userID uuid.UUID) error
}

type accountUserRepository struct{}

func NewAccountUserRepository() AccountUserRepository {
	return &accountUserRepository{}
}

func (r *accountUserRepository) Create(au *models.AccountUser) error {
	return facades.Orm().Query().Create(au)
}

func (r *accountUserRepository) FindByAccountID(accountID uuid.UUID) ([]models.AccountUser, error) {
	var members []models.AccountUser
	err := facades.Orm().Query().
		Where("account_id = ? AND deleted_at IS NULL", accountID).
		Find(&members)
	return members, err
}

func (r *accountUserRepository) FindByAccountAndUser(accountID, userID uuid.UUID) (*models.AccountUser, error) {
	var au models.AccountUser
	err := facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		First(&au)
	if err != nil {
		return nil, err
	}
	if au.ID == uuid.Nil {
		return nil, nil
	}
	return &au, nil
}

func (r *accountUserRepository) FindByAccountAndUserIncludeDeleted(accountID, userID uuid.UUID) (*models.AccountUser, error) {
	var au models.AccountUser
	err := facades.Orm().Query().
		Where("account_id = ? AND user_id = ?", accountID, userID).
		First(&au)
	if err != nil {
		return nil, err
	}
	if au.ID == uuid.Nil {
		return nil, nil
	}
	return &au, nil
}

func (r *accountUserRepository) FindByUserID(userID uuid.UUID) ([]models.AccountUser, error) {
	var memberships []models.AccountUser
	err := facades.Orm().Query().
		Where("user_id = ? AND deleted_at IS NULL", userID).
		Find(&memberships)
	return memberships, err
}

func (r *accountUserRepository) UpdateField(id uuid.UUID, field string, value interface{}) error {
	_, err := facades.Orm().Query().
		Model(&models.AccountUser{}).
		Where("id = ?", id).
		Update(field, value)
	return err
}

func (r *accountUserRepository) SoftDeleteByAccountAndUser(accountID, userID uuid.UUID) error {
	now := time.Now()
	_, err := facades.Orm().Query().
		Model(&models.AccountUser{}).
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		Update("deleted_at", now)
	return err
}
