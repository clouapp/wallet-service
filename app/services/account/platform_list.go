package account

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// ListForPlatform returns one page of every account for a platform admin.
// S3.4.1 names accounts.view on /v1/platform/accounts. This branch has no
// platform permission catalog, so the gate is the platform_admins row the
// other /v1/platform account routes use. A caller who is not a platform admin
// is ErrPlatformViewForbidden and the store is not read. Rows are newest
// created_at first. The plan does not name a sort.
func (s *Service) ListForPlatform(ctx context.Context, actorID uuid.UUID, limit, offset int) ([]models.Account, int64, error) {
	if ctx == nil {
		return nil, 0, fmt.Errorf("list accounts: context is required")
	}
	if actorID == uuid.Nil {
		return nil, 0, fmt.Errorf("list accounts: actor is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list accounts: limit and offset are invalid")
	}
	if s == nil {
		return nil, 0, fmt.Errorf("account service: service is required")
	}
	if err := s.requireAccounts(); err != nil {
		return nil, 0, err
	}
	if s.admins == nil {
		return nil, 0, fmt.Errorf("list accounts: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return nil, 0, err
	}
	if !admin {
		return nil, 0, ErrPlatformViewForbidden
	}
	return s.accounts.List(ctx, limit, offset)
}
