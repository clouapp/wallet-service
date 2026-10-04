package account

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// AttachOwnerForPlatform links an existing user to an account as an active
// owner. S3.4.1 names POST /{id}/owners (attach owner — recovery)
// accounts.owners. A caller who is not a platform admin is
// ErrPlatformOwnersForbidden before the account is read. A missing account
// is ErrAccountNotFound. A missing user is ErrPlatformOwnerUserNotFound;
// this path does not create a user, send mail, or mint a token.
//
// The body field is email because account member add names email.
// Rank is not applied: a platform admin may attach an owner without being
// a member of the account. A frozen account is not refused.
//
// changed is false when the user is already the active owner. That call
// does not write a membership and does not write an activity row. A create
// or a restore writes once. account_users is already captured with
// account_id, user_id, role, and status, so that write is the one activity
// row. There is no membership-attach event name, and this path does not
// invent one or append a second account_activity row.
func (s *Service) AttachOwnerForPlatform(ctx context.Context, actorID, accountID uuid.UUID, email string) (member *models.AccountUser, changed bool, err error) {
	if ctx == nil {
		return nil, false, fmt.Errorf("attach account owner: context is required")
	}
	if actorID == uuid.Nil {
		return nil, false, fmt.Errorf("attach account owner: actor is required")
	}
	if accountID == uuid.Nil {
		return nil, false, fmt.Errorf("attach account owner: account id is required")
	}
	if s == nil {
		return nil, false, fmt.Errorf("account service: service is required")
	}
	if err := s.requireAccounts(); err != nil {
		return nil, false, err
	}
	if s.memberships == nil {
		return nil, false, fmt.Errorf("attach account owner: memberships are required")
	}
	if s.users == nil {
		return nil, false, fmt.Errorf("attach account owner: users are required")
	}
	if s.admins == nil {
		return nil, false, fmt.Errorf("attach account owner: platform admins are required")
	}
	admin, err := s.admins.Contains(ctx, actorID)
	if err != nil {
		return nil, false, err
	}
	if !admin {
		return nil, false, ErrPlatformOwnersForbidden
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, false, fmt.Errorf("attach account owner: email is required")
	}
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, false, ErrAccountNotFound
		}
		return nil, false, err
	}
	if account == nil || account.ID == uuid.Nil {
		return nil, false, ErrAccountNotFound
	}
	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, false, ErrPlatformOwnerUserNotFound
		}
		return nil, false, err
	}
	if user == nil || user.ID == uuid.Nil {
		return nil, false, ErrPlatformOwnerUserNotFound
	}
	existing, err := s.memberships.FindForOwnerAttach(ctx, account.ID, user.ID)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, false, err
	}
	if existing == nil || errors.Is(err, models.ErrRepositoryNotFound) {
		addedBy := actorID
		created := &models.AccountUser{
			ID:        uuid.New(),
			AccountID: account.ID,
			UserID:    user.ID,
			Role:      models.AccountRoleOwner,
			Status:    models.MembershipStatusActive,
			AddedBy:   &addedBy,
		}
		if err := s.memberships.Create(ctx, created); err != nil {
			return nil, false, err
		}
		member, err = s.storedOwner(ctx, account.ID, user)
		if err != nil {
			return nil, false, err
		}
		return member, true, nil
	}
	if activeOwnerMembership(existing) {
		existing.User = ownerPublicUser(user)
		return existing, false, nil
	}
	if err := s.memberships.ActivateOwner(ctx, existing.ID); err != nil {
		return nil, false, err
	}
	member, err = s.storedOwner(ctx, account.ID, user)
	if err != nil {
		return nil, false, err
	}
	return member, true, nil
}

func (s *Service) storedOwner(ctx context.Context, accountID uuid.UUID, user *models.User) (*models.AccountUser, error) {
	if user == nil || user.ID == uuid.Nil {
		return nil, fmt.Errorf("attach account owner: user is required")
	}
	stored, err := s.memberships.FindByAccountAndUser(ctx, accountID, user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, fmt.Errorf("attach account owner: membership was not stored")
		}
		return nil, err
	}
	if stored == nil || stored.ID == uuid.Nil {
		return nil, fmt.Errorf("attach account owner: membership was not stored")
	}
	if stored.AccountID != accountID || stored.UserID != user.ID {
		return nil, fmt.Errorf("attach account owner: membership is on another account")
	}
	if stored.Role != models.AccountRoleOwner || !models.MembershipGrantsAccess(stored.Status) {
		return nil, fmt.Errorf("attach account owner: membership was not stored as an active owner")
	}
	stored.User = ownerPublicUser(user)
	return stored, nil
}

func activeOwnerMembership(row *models.AccountUser) bool {
	if row == nil || row.ID == uuid.Nil || row.DeletedAt != nil {
		return false
	}
	if row.Role != models.AccountRoleOwner {
		return false
	}
	switch row.Status {
	case models.MembershipStatusActive, "":
		return true
	default:
		return false
	}
}

func ownerPublicUser(user *models.User) *models.User {
	if user == nil {
		return nil
	}
	return &models.User{
		ID:          user.ID,
		Email:       user.Email,
		FullName:    user.FullName,
		Status:      user.Status,
		SuspendedAt: user.SuspendedAt,
		TotpEnabled: user.TotpEnabled,
	}
}
