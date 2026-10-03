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

// Reader pages one account's activity, newest first.
type Reader interface {
	List(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountActivity, int64, error)
}

// Writer appends one row. Within runs fn in one transaction; Append joins a
// transaction already stored on ctx.
type Writer interface {
	Within(ctx context.Context, fn func(context.Context) error) error
	Append(ctx context.Context, row models.AccountActivity) error
}

// Service lists account activity for roles that hold activity.read.
type Service struct {
	rows Reader
}

// NewService builds the account activity reader. The writer is the same
// repository, injected into the services that change members, settings and flags.
func NewService(rows Reader) *Service {
	if rows == nil {
		panic("account activity service: reader is required")
	}
	return &Service{rows: rows}
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
