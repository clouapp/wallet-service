package activity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

// ErrReadForbidden is a member who does not hold activity.read.
var ErrReadForbidden = errors.New("you do not have permission to view account activity")

// ErrNotFound is a row this account cannot see: unknown id, another account,
// or a platform row (null account id). The three cases are the same error.
var ErrNotFound = errors.New("activity not found")

// ErrPlatformForbidden is a caller who is not a platform admin. The plan names
// audit.view; this branch has no platform permission catalog, so the gate is
// the platform_admins row the other /v1/platform routes use.
var ErrPlatformForbidden = errors.New("you do not have permission to view platform activity")

// Reader pages activity, newest first, and loads one row of an account.
type Reader interface {
	List(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountActivity, int64, error)
	ListPlatform(ctx context.Context, limit, offset int) ([]models.AccountActivity, int64, error)
	Find(ctx context.Context, accountID, id uuid.UUID) (*models.AccountActivity, error)
}

// PlatformAdmins reports whether a user may read the platform trail.
type PlatformAdmins interface {
	Contains(ctx context.Context, userID uuid.UUID) (bool, error)
}

// Writer appends one row. Within runs fn in one transaction; Append joins a
// transaction already stored on ctx.
type Writer interface {
	Within(ctx context.Context, fn func(context.Context) error) error
	Append(ctx context.Context, row models.AccountActivity) error
}

// Service lists account activity for roles that hold activity.read.
type Service struct {
	rows   Reader
	admins PlatformAdmins
}

// NewService builds the account activity reader. The writer is the same
// repository, injected into the services that change members, settings and flags.
func NewService(rows Reader) *Service {
	if rows == nil {
		panic("account activity service: reader is required")
	}
	return &Service{rows: rows}
}

// WithPlatformAdmins attaches the platform-admin lookup ListPlatform uses.
func (s *Service) WithPlatformAdmins(admins PlatformAdmins) *Service {
	if s == nil {
		return nil
	}
	s.admins = admins
	return s
}

// List returns one page, newest first. Owner, admin and auditor may read.
// User is ErrReadForbidden.
func (s *Service) List(ctx context.Context, accountID uuid.UUID, role string, limit, offset int) ([]models.AccountActivity, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("account activity: context is required")
	}
	if accountID == uuid.Nil {
		return nil, 0, fmt.Errorf("account activity: account id is required")
	}
	if !policies.MayReadActivity(role) {
		return nil, 0, ErrReadForbidden
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("account activity: limit and offset are invalid")
	}
	rows, total, err := s.rows.List(ctx, accountID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []models.AccountActivity{}
	}
	return rows, total, nil
}

// Get returns one row of this account. The gate is the same activity.read
// check as List: owner, admin and auditor may read, and user is
// ErrReadForbidden before any lookup. A row from another account, a platform
// row, or an unknown id is ErrNotFound.
func (s *Service) Get(ctx context.Context, accountID uuid.UUID, role string, activityID uuid.UUID) (models.AccountActivity, error) {
	if ctx == nil {
		return models.AccountActivity{}, fmt.Errorf("account activity: context is required")
	}
	if accountID == uuid.Nil {
		return models.AccountActivity{}, fmt.Errorf("account activity: account id is required")
	}
	if !policies.MayReadActivity(role) {
		return models.AccountActivity{}, ErrReadForbidden
	}
	if activityID == uuid.Nil {
		return models.AccountActivity{}, ErrNotFound
	}
	row, err := s.rows.Find(ctx, accountID, activityID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return models.AccountActivity{}, ErrNotFound
		}
		return models.AccountActivity{}, err
	}
	if row == nil || row.AccountID == nil || *row.AccountID != accountID {
		return models.AccountActivity{}, ErrNotFound
	}
	return *row, nil
}

// ListPlatform returns platform rows (null account id), newest first.
// A caller who is not a platform admin is ErrPlatformForbidden.
func (s *Service) ListPlatform(ctx context.Context, userID uuid.UUID, limit, offset int) ([]models.AccountActivity, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("platform activity: context is required")
	}
	if userID == uuid.Nil {
		return nil, 0, fmt.Errorf("platform activity: user id is required")
	}
	if s.admins == nil {
		return nil, 0, fmt.Errorf("platform activity: platform admins are required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("platform activity: limit and offset are invalid")
	}
	admin, err := s.admins.Contains(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	if !admin {
		return nil, 0, ErrPlatformForbidden
	}
	rows, total, err := s.rows.ListPlatform(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []models.AccountActivity{}
	}
	return rows, total, nil
}
