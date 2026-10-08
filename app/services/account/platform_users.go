package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// ListUsersForPlatform pages the users of one account for a platform admin.
// S3.4.1 names GET /{id}/users on /v1/platform/accounts and does not name
// fields, pagination, or sort. The page matches GET /v1/platform/users:
// newest user created_at first, then user id descending. Each row is the
// account member list. A missing account is ErrAccountNotFound before the
// platform-admin check, and its memberships are not read. A caller who is
// not a platform admin is ErrPlatformAccountUsersForbidden after that read.
func (s *Service) ListUsersForPlatform(ctx context.Context, actorID, accountID uuid.UUID, limit, offset int) ([]models.AccountUser, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list account users: context is required")
	}
	if actorID == uuid.Nil {
		return nil, 0, fmt.Errorf("list account users: actor is required")
	}
	if accountID == uuid.Nil {
		return nil, 0, fmt.Errorf("list account users: account id is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list account users: limit and offset are invalid")
	}
	if s == nil {
		return nil, 0, fmt.Errorf("account service: service is required")
	}
	if err := s.requireAccounts(); err != nil {
		return nil, 0, err
	}
	if s.memberships == nil {
		return nil, 0, fmt.Errorf("list account users: memberships are required")
	}
	if s.admins == nil {
		return nil, 0, fmt.Errorf("list account users: platform admins are required")
	}
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, 0, ErrAccountNotFound
		}
		return nil, 0, err
	}
	if account == nil || account.ID == uuid.Nil {
		return nil, 0, ErrAccountNotFound
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return nil, 0, err
	}
	if !admin {
		return nil, 0, ErrPlatformAccountUsersForbidden
	}
	rows, total, err := s.memberships.ListForPlatformAccount(ctx, account.ID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []models.AccountUser{}
	}
	for i := range rows {
		if rows[i].AccountID != account.ID {
			return nil, 0, fmt.Errorf("list account users: membership %s is on another account", rows[i].ID)
		}
		if rows[i].User == nil || rows[i].User.ID == uuid.Nil {
			return nil, 0, fmt.Errorf("list account users: membership %s has no user", rows[i].ID)
		}
	}
	return rows, total, nil
}
