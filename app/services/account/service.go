package account

import (
	"context"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

type Service struct {
	accountRepo     repositories.AccountRepository
	accountUserRepo repositories.AccountUserRepository
}

func NewService(accountRepo repositories.AccountRepository, accountUserRepo repositories.AccountUserRepository) *Service {
	return &Service{accountRepo: accountRepo, accountUserRepo: accountUserRepo}
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

func (s *Service) RemoveUser(ctx context.Context, accountID, userID uuid.UUID) error {
	return s.accountUserRepo.SoftDeleteByAccountAndUser(accountID, userID)
}
