package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// PlatformAdmins reports whether a dashboard user may change an account's
// lifecycle. S3.4.1 names accounts.lifecycle. This branch has no platform
// permission catalog, so a platform_admins row is the gate.
type PlatformAdmins interface {
	Contains(ctx context.Context, userID uuid.UUID) (bool, error)
}

// WithPlatformAdmins attaches the platform-admin lookup SetPlatformLifecycle uses.
func (s *Service) WithPlatformAdmins(admins PlatformAdmins) *Service {
	if s == nil {
		return nil
	}
	s.admins = admins
	return s
}

// SetPlatformLifecycle stores one account status for a platform admin.
// freeze stores frozen, unfreeze stores active, and archive stores archived.
// The same status again does not write. A caller who is not a platform admin
// is ErrPlatformLifecycleForbidden before the account is read. A missing
// account is ErrAccountNotFound and is not written.
func (s *Service) SetPlatformLifecycle(ctx context.Context, actorID, accountID uuid.UUID, status string) (*models.Account, error) {
	if ctx == nil {
		return nil, fmt.Errorf("platform account lifecycle: context is required")
	}
	if actorID == uuid.Nil {
		return nil, fmt.Errorf("platform account lifecycle: actor is required")
	}
	if accountID == uuid.Nil {
		return nil, fmt.Errorf("platform account lifecycle: account id is required")
	}
	switch status {
	case models.StatusActive, models.AccountStatusFrozen, models.AccountStatusArchived:
	default:
		return nil, ErrAccountStatus
	}
	if s == nil {
		return nil, fmt.Errorf("account service: service is required")
	}
	if err := s.requireAccounts(); err != nil {
		return nil, err
	}
	if s.admins == nil {
		return nil, fmt.Errorf("platform account lifecycle: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if !admin {
		return nil, ErrPlatformLifecycleForbidden
	}
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, ErrAccountNotFound
		}
		return nil, err
	}
	if account == nil || account.ID == uuid.Nil {
		return nil, ErrAccountNotFound
	}
	if account.Status == status {
		return account, nil
	}
	if err := s.accounts.SetStatus(ctx, account.ID, status); err != nil {
		return nil, err
	}
	account.Status = status
	return account, nil
}
