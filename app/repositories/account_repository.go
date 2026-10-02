package repositories

import (
	"strings"

	"github.com/google/uuid"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// AccountListFilter narrows PaginateByMember. Empty fields do not filter.
type AccountListFilter struct {
	Search      string
	Environment string
}

type AccountRepository interface {
	Create(account *models.Account) error
	FindByID(id uuid.UUID) (*models.Account, error)
	FindByIDs(ids []uuid.UUID) ([]models.Account, error)
	PaginateByMember(userID uuid.UUID, filter AccountListFilter, limit, offset int) ([]models.Account, int64, error)
	UpdateField(id uuid.UUID, field string, value interface{}) error
}

var likePatternEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

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

// PaginateByMember pages through the accounts the user is an active member
// of, ordered case-insensitively by name (the database collation may be "C")
// then by id so every page is stable.
func (r *accountRepository) PaginateByMember(userID uuid.UUID, filter AccountListFilter, limit, offset int) ([]models.Account, int64, error) {
	accounts := []models.Account{}
	q := memberAccountsQuery(userID, filter)

	total, err := q.Count()
	if err != nil {
		return nil, 0, err
	}
	if total == 0 || int64(offset) >= total {
		return accounts, total, nil
	}

	err = q.Order("LOWER(name) ASC, id ASC").Offset(offset).Limit(limit).Find(&accounts)
	return accounts, total, err
}

func memberAccountsQuery(userID uuid.UUID, filter AccountListFilter) contractsorm.Query {
	q := facades.Orm().Query().Model(&models.Account{}).
		Where("id IN (SELECT account_id FROM account_users WHERE user_id = ? AND deleted_at IS NULL)", userID)
	if filter.Environment != "" {
		q = q.Where("environment = ?", filter.Environment)
	}
	if filter.Search != "" {
		pattern := "%" + likePatternEscaper.Replace(filter.Search) + "%"
		q = q.Where("(name ILIKE ? OR CAST(id AS TEXT) ILIKE ?)", pattern, pattern)
	}
	return q
}

func (r *accountRepository) UpdateField(id uuid.UUID, field string, value interface{}) error {
	_, err := facades.Orm().Query().Model(&models.Account{}).Where("id = ?", id).Update(field, value)
	return err
}
