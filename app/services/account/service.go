package account

import (
	"context"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// AccountStore is the account writes this service performs.
type AccountStore interface {
	Create(ctx context.Context, account *models.Account) error
}

// MembershipStore is the membership reads and writes this service performs.
type MembershipStore interface {
	Create(ctx context.Context, au *models.AccountUser) error
	FindByAccountAndUser(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error)
	FindByAccountAndUserIncludeDeleted(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error)
	Restore(ctx context.Context, id uuid.UUID) error
	SetRole(ctx context.Context, id uuid.UUID, role string) error
	SoftDeleteByAccountAndUser(ctx context.Context, accountID, userID uuid.UUID) error
}

// Deps is everything Account needs. Optional collaborators stay nil fields;
// this service has none.
type Deps struct {
	Accounts    AccountStore
	Memberships MembershipStore
}

// Service creates accounts and manages their memberships.
type Service struct {
	accounts    AccountStore
	memberships MembershipStore
}

// NewService builds an account service from Deps.
func NewService(deps Deps) *Service {
	return &Service{accounts: deps.Accounts, memberships: deps.Memberships}
}

// Create inserts an active account and an owner membership.
func (s *Service) Create(ctx context.Context, name string, ownerID uuid.UUID) (*models.Account, error) {
	acc := &models.Account{ID: uuid.New(), Name: name, Status: "active"}
	if err := s.accounts.Create(ctx, acc); err != nil {
		return nil, err
	}
	membership := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: acc.ID,
		UserID:    ownerID,
		Role:      "owner",
	}
	if err := s.memberships.Create(ctx, membership); err != nil {
		return nil, err
	}
	return acc, nil
}

// GetUserRole returns the caller's role, or an empty string when they are not an active member.
func (s *Service) GetUserRole(ctx context.Context, accountID, userID uuid.UUID) (string, error) {
	au, err := s.memberships.FindByAccountAndUser(ctx, accountID, userID)
	if err != nil || au == nil {
		return "", nil
	}
	return au.Role, nil
}

// AddUser adds a member, restoring a soft-deleted membership when one exists.
func (s *Service) AddUser(ctx context.Context, accountID, userID uuid.UUID, role string, addedBy uuid.UUID) error {
	existing, err := s.memberships.FindByAccountAndUserIncludeDeleted(ctx, accountID, userID)
	if err == nil && existing != nil && existing.DeletedAt != nil {
		if err := s.memberships.Restore(ctx, existing.ID); err != nil {
			return err
		}
		return s.memberships.SetRole(ctx, existing.ID, role)
	}
	au := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    userID,
		Role:      role,
		AddedBy:   &addedBy,
	}
	return s.memberships.Create(ctx, au)
}

// RemoveUser soft-deletes the active membership.
func (s *Service) RemoveUser(ctx context.Context, accountID, userID uuid.UUID) error {
	return s.memberships.SoftDeleteByAccountAndUser(ctx, accountID, userID)
}
