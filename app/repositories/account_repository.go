package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	contractsorm "github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// AccountListFilter narrows PaginateByMember. Empty fields do not filter.
type AccountListFilter struct {
	Search      string
	Environment string
}

var likePatternEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// AccountRepository persists accounts.
type AccountRepository struct {
	db.Base
}

// NewAccountRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewAccountRepository(query contractsorm.Query) *AccountRepository {
	return &AccountRepository{Base: db.NewBase(query)}
}

// Create inserts an account.
func (r *AccountRepository) Create(ctx context.Context, account *models.Account) error {
	if account == nil {
		return fmt.Errorf("create account: account is nil")
	}
	if err := r.Query(ctx).Create(account); err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

// Exists reports whether the account id is stored. A nil id is absent.
func (r *AccountRepository) Exists(ctx context.Context, id uuid.UUID) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("account exists: repository is required")
	}
	if ctx == nil {
		return false, fmt.Errorf("account exists: context is required")
	}
	if id == uuid.Nil {
		return false, nil
	}
	found, err := r.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return false, nil
		}
		return false, err
	}
	return found != nil && found.ID == id, nil
}

// FindByID returns the account, or ErrRepositoryNotFound.
func (r *AccountRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Account, error) {
	var account models.Account
	if err := r.Query(ctx).Where("id = ?", id).First(&account); err != nil {
		return nil, fmt.Errorf("find account: %w", err)
	}
	if account.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &account, nil
}

// FindByIDs returns the accounts whose ids are in the list. An empty list is an empty result.
func (r *AccountRepository) FindByIDs(ctx context.Context, ids []uuid.UUID) ([]models.Account, error) {
	accounts := []models.Account{}
	if len(ids) == 0 {
		return accounts, nil
	}
	if err := r.Query(ctx).Where("id IN ?", ids).Find(&accounts); err != nil {
		return nil, fmt.Errorf("find accounts: %w", err)
	}
	return accounts, nil
}

// List pages every account, newest created_at first. Equal timestamps break
// on id descending so a page is stable. limit must be positive and offset
// must not be negative. Soft-deleted rows are not a column on accounts.
func (r *AccountRepository) List(ctx context.Context, limit, offset int) ([]models.Account, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list accounts: context is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list accounts: limit and offset are invalid")
	}
	total, err := r.Query(ctx).Model(&models.Account{}).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("list accounts: %w", err)
	}
	rows := []models.Account{}
	err = r.Query(ctx).
		Order("created_at DESC, id DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list accounts: %w", err)
	}
	return rows, total, nil
}

// PaginateByMember pages through the accounts the user is an active member
// of, ordered case-insensitively by name then by id so every page is stable.
func (r *AccountRepository) PaginateByMember(ctx context.Context, userID uuid.UUID, search, environment string, limit, offset int) ([]models.Account, int64, error) {
	accounts := []models.Account{}
	q := r.memberAccountsQuery(ctx, userID, AccountListFilter{Search: search, Environment: environment})

	total, err := q.Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count member accounts: %w", err)
	}
	if total == 0 || int64(offset) >= total {
		return accounts, total, nil
	}

	if err := q.Order("LOWER(name) ASC, id ASC").Offset(offset).Limit(limit).Find(&accounts); err != nil {
		return nil, 0, fmt.Errorf("list member accounts: %w", err)
	}
	return accounts, total, nil
}

func (r *AccountRepository) memberAccountsQuery(ctx context.Context, userID uuid.UUID, filter AccountListFilter) contractsorm.Query {
	q := r.Query(ctx).Model(&models.Account{}).
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

// SetName sets accounts.name.
func (r *AccountRepository) SetName(ctx context.Context, id uuid.UUID, name string) error {
	return r.updateColumn(ctx, id, "name", name, "set account name")
}

// SetViewAllWallets sets accounts.view_all_wallets.
func (r *AccountRepository) SetViewAllWallets(ctx context.Context, id uuid.UUID, viewAll bool) error {
	return r.updateColumn(ctx, id, "view_all_wallets", viewAll, "set account view_all_wallets")
}

// SetStatus sets accounts.status.
func (r *AccountRepository) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	return r.updateColumn(ctx, id, "status", status, "set account status")
}

// SetLinkedAccountID sets accounts.linked_account_id.
func (r *AccountRepository) SetLinkedAccountID(ctx context.Context, id, linkedID uuid.UUID) error {
	return r.updateColumn(ctx, id, "linked_account_id", linkedID, "set account linked account")
}

func (r *AccountRepository) updateColumn(ctx context.Context, id uuid.UUID, column string, value any, op string) error {
	if _, err := r.Query(ctx).Model(&models.Account{}).Where("id = ?", id).Update(column, value); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
