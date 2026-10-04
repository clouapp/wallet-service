package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// AccountUserRepository persists account memberships.
// FindByAccountAndUser and FindByUserID answer what the user can access and
// return active memberships only. IncludeDeleted and by-account listings
// return every status for membership management.
type AccountUserRepository struct {
	db.Base
}

// NewAccountUserRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewAccountUserRepository(query orm.Query) *AccountUserRepository {
	return &AccountUserRepository{Base: db.NewBase(query)}
}

// Create inserts a membership.
func (r *AccountUserRepository) Create(ctx context.Context, au *models.AccountUser) error {
	if au == nil {
		return fmt.Errorf("create account user: membership is nil")
	}
	if err := r.Query(ctx).Create(au); err != nil {
		return fmt.Errorf("create account user: %w", err)
	}
	return nil
}

// FindByAccountID returns active memberships of an account.
func (r *AccountUserRepository) FindByAccountID(ctx context.Context, accountID uuid.UUID) ([]models.AccountUser, error) {
	var members []models.AccountUser
	if err := r.Query(ctx).Where("account_id = ? AND deleted_at IS NULL", accountID).Find(&members); err != nil {
		return nil, fmt.Errorf("list account users: %w", err)
	}
	return members, nil
}

// FindByAccountAndUser returns the active membership, or ErrRepositoryNotFound.
func (r *AccountUserRepository) FindByAccountAndUser(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	return r.findMembership(ctx, accountID, userID, true)
}

// FindByAccountAndUserIncludeDeleted returns the membership even when soft-deleted, or ErrRepositoryNotFound.
func (r *AccountUserRepository) FindByAccountAndUserIncludeDeleted(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	return r.findMembership(ctx, accountID, userID, false)
}

func (r *AccountUserRepository) findMembership(ctx context.Context, accountID, userID uuid.UUID, activeOnly bool) (*models.AccountUser, error) {
	var au models.AccountUser
	q := r.Query(ctx).Where("account_id = ? AND user_id = ?", accountID, userID)
	if activeOnly {
		q = q.Where("deleted_at IS NULL AND status = ?", models.StatusActive)
	}
	if err := q.First(&au); err != nil {
		return nil, fmt.Errorf("find account user: %w", err)
	}
	if au.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &au, nil
}

// FindByUserID returns the user's active memberships.
func (r *AccountUserRepository) FindByUserID(ctx context.Context, userID uuid.UUID) ([]models.AccountUser, error) {
	var memberships []models.AccountUser
	if err := r.Query(ctx).Where("user_id = ? AND deleted_at IS NULL AND status = ?", userID, models.StatusActive).Find(&memberships); err != nil {
		return nil, fmt.Errorf("list user memberships: %w", err)
	}
	return memberships, nil
}

// RolesForUserAccounts returns the stored account_users.role for each active
// membership of userID among accountIDs. The string is returned as stored
// (owner, admin, auditor, user). fix/security-s7, which renames a stored
// viewer to auditor, is not on this branch, so a viewer row stays viewer.
// An empty accountIDs list is an empty map. A duplicate active membership
// for one account is an error.
func (r *AccountUserRepository) RolesForUserAccounts(ctx context.Context, userID uuid.UUID, accountIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("list membership roles: user id is required")
	}
	roles := map[uuid.UUID]string{}
	if len(accountIDs) == 0 {
		return roles, nil
	}
	var memberships []models.AccountUser
	if err := r.Query(ctx).
		Where("user_id = ? AND deleted_at IS NULL AND account_id IN ?", userID, accountIDs).
		Find(&memberships); err != nil {
		return nil, fmt.Errorf("list membership roles: %w", err)
	}
	for _, membership := range memberships {
		if strings.TrimSpace(membership.Role) == "" {
			return nil, fmt.Errorf("list membership roles: account %s has an empty role", membership.AccountID)
		}
		if _, exists := roles[membership.AccountID]; exists {
			return nil, fmt.Errorf("list membership roles: account %s has more than one active membership", membership.AccountID)
		}
		roles[membership.AccountID] = membership.Role
	}
	return roles, nil
}

// PaginateByUserID pages the user's active memberships.
func (r *AccountUserRepository) PaginateByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]models.AccountUser, int64, error) {
	return r.paginate(ctx, "user_id = ? AND deleted_at IS NULL AND status = '"+models.StatusActive+"'", userID, limit, offset)
}

// PaginateByAccountID pages an account's active memberships.
func (r *AccountUserRepository) PaginateByAccountID(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountUser, int64, error) {
	return r.paginate(ctx, "account_id = ? AND deleted_at IS NULL", accountID, limit, offset)
}

// ListForPlatformAccount pages one account's memberships for a platform
// admin. Soft-deleted rows stay out, matching PaginateByAccountID, and every
// stored status stays in. The page is newest user created_at first, then
// user id descending, matching GET /v1/platform/users. The user preload
// selects only the columns that list returns.
func (r *AccountUserRepository) ListForPlatformAccount(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountUser, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list account users: context is required")
	}
	if accountID == uuid.Nil {
		return nil, 0, fmt.Errorf("list account users: account id is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list account users: limit and offset are invalid")
	}
	total, err := r.Query(ctx).Model(&models.AccountUser{}).
		Where("account_id = ? AND deleted_at IS NULL", accountID).
		Count()
	if err != nil {
		return nil, 0, fmt.Errorf("list account users: %w", err)
	}
	rows := []models.AccountUser{}
	err = r.Query(ctx).
		With("User", func(query orm.Query) orm.Query {
			return query.Select("id", "email", "full_name", "status", "suspended_at", "totp_enabled")
		}).
		Where("account_id = ? AND deleted_at IS NULL", accountID).
		OrderByRaw("(SELECT users.created_at FROM users WHERE users.id = account_users.user_id) DESC, (SELECT users.id FROM users WHERE users.id = account_users.user_id) DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list account users: %w", err)
	}
	return rows, total, nil
}

func (r *AccountUserRepository) paginate(ctx context.Context, where string, id uuid.UUID, limit, offset int) ([]models.AccountUser, int64, error) {
	total, err := r.Query(ctx).Model(&models.AccountUser{}).Where(where, id).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count account users: %w", err)
	}
	var members []models.AccountUser
	if err := r.Query(ctx).Where(where, id).Offset(offset).Limit(limit).Find(&members); err != nil {
		return nil, 0, fmt.Errorf("list account users: %w", err)
	}
	return members, total, nil
}

// Restore clears deleted_at on a membership.
func (r *AccountUserRepository) Restore(ctx context.Context, id uuid.UUID) error {
	if _, err := r.Query(ctx).Model(&models.AccountUser{}).Where("id = ?", id).Update("deleted_at", nil); err != nil {
		return fmt.Errorf("restore account user: %w", err)
	}
	return nil
}

// SetRole sets account_users.role.
func (r *AccountUserRepository) SetRole(ctx context.Context, id uuid.UUID, role string) error {
	if id == uuid.Nil {
		return fmt.Errorf("set account user role: id is required")
	}
	if strings.TrimSpace(role) == "" {
		return fmt.Errorf("set account user role: role is required")
	}
	if _, err := r.Query(ctx).Model(&models.AccountUser{}).Where("id = ?", id).Update("role", role); err != nil {
		return fmt.Errorf("set account user role: %w", err)
	}
	return nil
}

// SetStatus sets account_users.status.
func (r *AccountUserRepository) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	if id == uuid.Nil {
		return fmt.Errorf("set account user status: id is required")
	}
	if strings.TrimSpace(status) == "" {
		return fmt.Errorf("set account user status: status is required")
	}
	if _, err := r.Query(ctx).Model(&models.AccountUser{}).Where("id = ?", id).Update("status", status); err != nil {
		return fmt.Errorf("set account user status: %w", err)
	}
	return nil
}

// CountActiveByRole counts memberships of role that still grant access.
// Rows are locked for update so a last-owner check and the write that follows
// share one transaction. A suspended membership does not count.
func (r *AccountUserRepository) CountActiveByRole(ctx context.Context, accountID uuid.UUID, role string) (int64, error) {
	if accountID == uuid.Nil {
		return 0, fmt.Errorf("count account users: account id is required")
	}
	if strings.TrimSpace(role) == "" {
		return 0, fmt.Errorf("count account users: role is required")
	}
	var members []models.AccountUser
	err := r.Query(ctx).
		Where(
			"account_id = ? AND role = ? AND deleted_at IS NULL AND (status = ? OR status = '')",
			accountID,
			role,
			models.MembershipStatusActive,
		).
		LockForUpdate().
		Find(&members)
	if err != nil {
		return 0, fmt.Errorf("count account users: %w", err)
	}
	return int64(len(members)), nil
}

// Within runs fn inside one transaction. Queries made with the callback
// context join that transaction.
func (r *AccountUserRepository) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("account user transaction: callback is required")
	}
	return r.Transaction(ctx, func(tx orm.Query) error {
		return fn(db.WithTx(ctx, tx))
	})
}

// SoftDeleteByAccountAndUser sets deleted_at on the active membership.
func (r *AccountUserRepository) SoftDeleteByAccountAndUser(ctx context.Context, accountID, userID uuid.UUID) error {
	now := time.Now()
	if _, err := r.Query(ctx).Model(&models.AccountUser{}).
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		Update("deleted_at", now); err != nil {
		return fmt.Errorf("soft delete account user: %w", err)
	}
	return nil
}
