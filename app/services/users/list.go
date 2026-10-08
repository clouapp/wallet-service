package users

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// List returns one page of users for a platform admin. S3.4.1 names
// users.view; this branch has no platform permission catalog, so the gate is
// the platform_admins row the other /v1/platform user routes use. A caller
// who is not a platform admin is ErrViewForbidden and the store is not read.
// Rows are newest created_at first. The plan does not name a sort.
func (s *Service) List(ctx context.Context, actorID uuid.UUID, limit, offset int) ([]models.User, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list users: context is required")
	}
	if actorID == uuid.Nil {
		return nil, 0, fmt.Errorf("list users: actor is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list users: limit and offset are invalid")
	}
	if s == nil || s.store == nil {
		return nil, 0, fmt.Errorf("users service: users repository is required")
	}
	if s.admins == nil {
		return nil, 0, fmt.Errorf("list users: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return nil, 0, err
	}
	if !admin {
		return nil, 0, ErrViewForbidden
	}
	return s.store.List(ctx, limit, offset)
}
