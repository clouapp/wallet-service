package account

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/repositories"
)

var ErrMemberNotFound = errors.New("account member not found")

type Service struct {
	accountRepo     repositories.AccountRepository
	accountUserRepo repositories.AccountUserRepository
	tokenRepo       repositories.AccessTokenRepository
	inviteRepo      repositories.AccountInviteRepository
}

func NewService(accountRepo repositories.AccountRepository, accountUserRepo repositories.AccountUserRepository, tokenRepo repositories.AccessTokenRepository) *Service {
	return &Service{
		accountRepo:     accountRepo,
		accountUserRepo: accountUserRepo,
		tokenRepo:       tokenRepo,
		inviteRepo:      repositories.NewAccountInviteRepository(),
	}
}

func (s *Service) Create(ctx context.Context, name string, ownerID uuid.UUID) (*models.Account, error) {
	acc := &models.Account{ID: uuid.New(), Name: name, Status: "active"}
	if err := s.accountRepo.Create(acc); err != nil {
		return nil, err
	}
	membership := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: acc.ID,
		UserID:    ownerID,
		Role:      "owner",
	}
	if err := s.accountUserRepo.Create(membership); err != nil {
		return nil, err
	}
	return acc, nil
}

func (s *Service) GetUserRole(ctx context.Context, accountID, userID uuid.UUID) (string, error) {
	au, err := s.accountUserRepo.FindByAccountAndUser(accountID, userID)
	if err != nil {
		return "", nil
	}
	if au == nil {
		return "", nil
	}
	return au.Role, nil
}

func (s *Service) AddUser(ctx context.Context, accountID, userID uuid.UUID, role string, addedBy uuid.UUID) error {
	if s == nil || s.accountUserRepo == nil {
		return errors.New("account user repository is required")
	}
	actor, err := s.accountUserRepo.FindByAccountAndUser(accountID, addedBy)
	if err != nil {
		return err
	}
	if actor == nil || !policies.MayGrant(actor.Role, role) {
		return policies.ErrRoleAbove
	}

	existing, err := s.accountUserRepo.FindByAccountAndUserIncludeDeleted(accountID, userID)
	if err == nil && existing != nil && existing.DeletedAt != nil {
		if err := s.accountUserRepo.UpdateField(existing.ID, "deleted_at", nil); err != nil {
			return err
		}
		return s.accountUserRepo.UpdateField(existing.ID, "role", role)
	}
	au := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    userID,
		Role:      role,
		AddedBy:   &addedBy,
	}
	return s.accountUserRepo.Create(au)
}

func (s *Service) RemoveUser(ctx context.Context, accountID, actorID, userID uuid.UUID) error {
	if s == nil || s.accountUserRepo == nil || s.tokenRepo == nil {
		return errors.New("account membership repositories are required")
	}
	members, err := s.accountUserRepo.FindByAccountID(accountID)
	if err != nil {
		return err
	}
	var actorRole, targetRole string
	var targetFound bool
	owners := 0
	for i := range members {
		member := members[i]
		if member.Role == models.AccountRoleOwner {
			owners++
		}
		if member.UserID == actorID {
			actorRole = member.Role
		}
		if member.UserID == userID {
			targetFound = true
			targetRole = member.Role
		}
	}
	if !targetFound {
		return ErrMemberNotFound
	}
	if err := policies.RefuseMemberRemoval(actorID, userID, actorRole, targetRole, owners); err != nil {
		return err
	}
	if err := s.accountUserRepo.SoftDeleteByAccountAndUser(accountID, userID); err != nil {
		return err
	}
	return s.tokenRepo.DeleteByAccountAndCreator(accountID, userID)
}
