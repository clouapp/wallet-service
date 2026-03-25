package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type AccountRepository interface {
	Create(account *models.Account) error
	FindByID(id uuid.UUID) (*models.Account, error)
	FindByIDs(ids []uuid.UUID) ([]models.Account, error)
	UpdateField(id uuid.UUID, field string, value interface{}) error
}

type accountRepository struct{}

func NewAccountRepository() AccountRepository {
	return &accountRepository{}
}

func (r *accountRepository) Create(account *models.Account) error {
	return facades.Orm().Query().Create(account)
}

func (r *accountRepository) FindByID(id uuid.UUID) (*models.Account, error) {
	var account models.Account
	err := facades.Orm().Query().Where("id = ?", id).First(&account)
	if err != nil {
		return nil, err
	}
	if account.ID == uuid.Nil {
		return nil, nil
	}
	return &account, nil
}

func (r *accountRepository) FindByIDs(ids []uuid.UUID) ([]models.Account, error) {
	var accounts []models.Account
	if len(ids) == 0 {
		return accounts, nil
	}
	err := facades.Orm().Query().Where("id IN ?", ids).Find(&accounts)
	return accounts, err
}

func (r *accountRepository) UpdateField(id uuid.UUID, field string, value interface{}) error {
	_, err := facades.Orm().Query().Model(&models.Account{}).Where("id = ?", id).Update(field, value)
	return err
}
