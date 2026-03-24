package account

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
)

type Service struct{}

func NewService() *Service { return &Service{} }

func (s *Service) Create(ctx context.Context, name string, ownerID uuid.UUID) (*models.Account, error) {
	acc := &models.Account{ID: uuid.New(), Name: name, Status: "active"}
	if err := facades.Orm().Query().Create(acc); err != nil {
		return nil, err
	}
	membership := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: acc.ID,
		UserID:    ownerID,
		Role:      "owner",
	}
	if err := facades.Orm().Query().Create(membership); err != nil {
		return nil, err
	}
	return acc, nil
}

func (s *Service) GetUserRole(ctx context.Context, accountID, userID uuid.UUID) (string, error) {
	var au models.AccountUser
	err := facades.Orm().Query().
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		First(&au)
	if err != nil {
		return "", nil // not a member
	}
	return au.Role, nil
}

func (s *Service) AddUser(ctx context.Context, accountID, userID uuid.UUID, role string, addedBy uuid.UUID) error {
	// Re-add: clear deleted_at if row exists
	var existing models.AccountUser
	err := facades.Orm().Query().
		Where("account_id = ? AND user_id = ?", accountID, userID).
		First(&existing)
	if err == nil && existing.DeletedAt != nil {
		_, err1 := facades.Orm().Query().
			Model(&existing).
			Where("id = ?", existing.ID).
			Update("deleted_at", nil)
		if err1 != nil {
			return err1
		}
		_, err2 := facades.Orm().Query().
			Model(&existing).
			Where("id = ?", existing.ID).
			Update("role", role)
		return err2
	}
	au := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    userID,
		Role:      role,
		AddedBy:   &addedBy,
	}
	return facades.Orm().Query().Create(au)
}

func (s *Service) RemoveUser(ctx context.Context, accountID, userID uuid.UUID) error {
	now := time.Now()
	_, err := facades.Orm().Query().
		Model(&models.AccountUser{}).
		Where("account_id = ? AND user_id = ? AND deleted_at IS NULL", accountID, userID).
		Update("deleted_at", now)
	return err
}
