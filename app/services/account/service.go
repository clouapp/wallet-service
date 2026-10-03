package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
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
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
	SoftDeleteByAccountAndUser(ctx context.Context, accountID, userID uuid.UUID) error
	CountActiveByRole(ctx context.Context, accountID uuid.UUID, role string) (int64, error)
	Within(ctx context.Context, fn func(context.Context) error) error
}

// TokenStore revokes API tokens a member created for one account.
type TokenStore interface {
	DeleteByAccountAndCreator(ctx context.Context, accountID, createdBy uuid.UUID) error
}

// Deps is everything Account needs. Tokens is required for member updates
// that revoke API tokens. Older callers that only create accounts may leave it nil.
type Deps struct {
	Accounts    AccountStore
	Memberships MembershipStore
	Tokens      TokenStore
}

// MemberChange is a PATCH of one membership. A nil field is left as stored.
type MemberChange struct {
	Role   *string
	Status *string
}

// Service creates accounts and manages their memberships.
type Service struct {
	accounts    AccountStore
	memberships MembershipStore
	tokens      TokenStore
}

// NewService builds an account service from Deps.
func NewService(deps Deps) *Service {
	return &Service{accounts: deps.Accounts, memberships: deps.Memberships, tokens: deps.Tokens}
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
		Role:      policies.AccountOwnerRole(),
		Status:    models.MembershipStatusActive,
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
		if err := s.memberships.SetRole(ctx, existing.ID, role); err != nil {
			return err
		}
		return s.memberships.SetStatus(ctx, existing.ID, models.MembershipStatusActive)
	}
	au := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    userID,
		Role:      role,
		Status:    models.MembershipStatusActive,
		AddedBy:   &addedBy,
	}
	return s.memberships.Create(ctx, au)
}

// RemoveUser soft-deletes the active membership. It does not check rank or
// revoke tokens. RemoveMember is the dashboard path.
func (s *Service) RemoveUser(ctx context.Context, accountID, userID uuid.UUID) error {
	return s.memberships.SoftDeleteByAccountAndUser(ctx, accountID, userID)
}

// UpdateMember changes role and/or status. Suspending revokes the API tokens
// that member created for this account. The caller cannot change themselves,
// grant a role above their own, act on a higher rank, or leave the account
// without an owner.
func (s *Service) UpdateMember(ctx context.Context, accountID, actorID, targetID uuid.UUID, change MemberChange) (*models.AccountUser, error) {
	if err := validateMemberIDs(accountID, actorID, targetID); err != nil {
		return nil, err
	}
	if err := validateMemberChange(change); err != nil {
		return nil, err
	}
	if err := s.requireTokens(); err != nil {
		return nil, err
	}

	var updated *models.AccountUser
	err := s.memberships.Within(ctx, func(ctx context.Context) error {
		actor, target, err := s.loadActorAndTarget(ctx, accountID, actorID, targetID)
		if err != nil {
			return err
		}
		if err := s.authorizeMemberChange(ctx, accountID, actor, target, change); err != nil {
			return err
		}
		if change.Role != nil && *change.Role != target.Role {
			if err := s.memberships.SetRole(ctx, target.ID, *change.Role); err != nil {
				return err
			}
		}
		if change.Status != nil && *change.Status != target.Status {
			if err := s.memberships.SetStatus(ctx, target.ID, *change.Status); err != nil {
				return err
			}
		}
		if change.Status != nil && *change.Status == models.MembershipStatusSuspended {
			if err := s.tokens.DeleteByAccountAndCreator(ctx, accountID, targetID); err != nil {
				return err
			}
		}
		updated, err = s.memberships.FindByAccountAndUser(ctx, accountID, targetID)
		if err != nil {
			return err
		}
		if updated == nil {
			return ErrMemberNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// RemoveMember soft-deletes a membership and revokes the API tokens that member
// created for this account. The caller cannot remove themselves, a higher rank,
// or the last owner.
func (s *Service) RemoveMember(ctx context.Context, accountID, actorID, targetID uuid.UUID) error {
	if err := validateMemberIDs(accountID, actorID, targetID); err != nil {
		return err
	}
	if err := s.requireTokens(); err != nil {
		return err
	}
	return s.memberships.Within(ctx, func(ctx context.Context) error {
		actor, target, err := s.loadActorAndTarget(ctx, accountID, actorID, targetID)
		if err != nil {
			return err
		}
		if err := s.authorizeMemberRemoval(ctx, accountID, actor, target); err != nil {
			return err
		}
		if err := s.tokens.DeleteByAccountAndCreator(ctx, accountID, targetID); err != nil {
			return err
		}
		return s.memberships.SoftDeleteByAccountAndUser(ctx, accountID, targetID)
	})
}

func (s *Service) loadActorAndTarget(ctx context.Context, accountID, actorID, targetID uuid.UUID) (*models.AccountUser, *models.AccountUser, error) {
	actor, err := s.membership(ctx, accountID, actorID)
	if err != nil {
		if errors.Is(err, ErrMemberNotFound) {
			return nil, nil, ErrManageMembers
		}
		return nil, nil, err
	}
	if !policies.ManagesMembers(actor.Role) {
		return nil, nil, ErrManageMembers
	}
	target, err := s.membership(ctx, accountID, targetID)
	if err != nil {
		return nil, nil, err
	}
	return actor, target, nil
}

func (s *Service) authorizeMemberChange(ctx context.Context, accountID uuid.UUID, actor, target *models.AccountUser, change MemberChange) error {
	if removesActiveOwner(target, change) {
		if err := s.guardLastOwner(ctx, accountID); err != nil {
			return err
		}
	}
	if actor.UserID == target.UserID {
		return ErrSelfMembership
	}
	if !policies.MayActOn(actor.Role, target.Role) {
		return ErrActOnMember
	}
	if change.Role != nil && !policies.MayGrant(actor.Role, *change.Role) {
		return ErrGrantRole
	}
	return nil
}

func (s *Service) authorizeMemberRemoval(ctx context.Context, accountID uuid.UUID, actor, target *models.AccountUser) error {
	if policies.IsAccountOwner(target.Role) && models.MembershipGrantsAccess(target.Status) {
		if err := s.guardLastOwner(ctx, accountID); err != nil {
			return err
		}
	}
	if actor.UserID == target.UserID {
		return ErrSelfMembership
	}
	if !policies.MayActOn(actor.Role, target.Role) {
		return ErrActOnMember
	}
	return nil
}

func (s *Service) guardLastOwner(ctx context.Context, accountID uuid.UUID) error {
	owners, err := s.memberships.CountActiveByRole(ctx, accountID, policies.AccountOwnerRole())
	if err != nil {
		return err
	}
	if owners <= 1 {
		return ErrLastOwner
	}
	return nil
}

func (s *Service) membership(ctx context.Context, accountID, userID uuid.UUID) (*models.AccountUser, error) {
	member, err := s.memberships.FindByAccountAndUser(ctx, accountID, userID)
	if err != nil {
		if errors.Is(err, models.ErrRepositoryNotFound) {
			return nil, ErrMemberNotFound
		}
		return nil, err
	}
	if member == nil {
		return nil, ErrMemberNotFound
	}
	return member, nil
}

func (s *Service) requireTokens() error {
	if s.tokens == nil {
		return fmt.Errorf("account service: access tokens repository is required")
	}
	if s.memberships == nil {
		return fmt.Errorf("account service: memberships repository is required")
	}
	return nil
}

func validateMemberIDs(accountID, actorID, targetID uuid.UUID) error {
	if accountID == uuid.Nil || actorID == uuid.Nil || targetID == uuid.Nil {
		return fmt.Errorf("member change: account, actor and target are required")
	}
	return nil
}

func validateMemberChange(change MemberChange) error {
	if change.Role == nil && change.Status == nil {
		return ErrMemberChangeEmpty
	}
	if change.Role != nil && !policies.KnownAccountRole(*change.Role) {
		return ErrMemberRole
	}
	if change.Status != nil && *change.Status != models.MembershipStatusActive && *change.Status != models.MembershipStatusSuspended {
		return ErrMemberStatus
	}
	return nil
}

func removesActiveOwner(target *models.AccountUser, change MemberChange) bool {
	if target == nil || !policies.IsAccountOwner(target.Role) || !models.MembershipGrantsAccess(target.Status) {
		return false
	}
	if change.Status != nil && *change.Status == models.MembershipStatusSuspended {
		return true
	}
	if change.Role != nil && !policies.IsAccountOwner(*change.Role) {
		return true
	}
	return false
}
