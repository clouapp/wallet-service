package seeders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

// AccountUserSeeder links users to the prod and test accounts with roles.
type AccountUserSeeder struct{}

func (s *AccountUserSeeder) Signature() string {
	return "AccountUserSeeder"
}

// Run links users to prod and test accounts with roles.
func (s *AccountUserSeeder) Run() error {
	ctx := context.Background()
	members := []struct {
		id        uuid.UUID
		accountID uuid.UUID
		userID    uuid.UUID
		role      string
	}{
		{uuid.MustParse("00000000-0000-0000-0000-000000000030"), acmeAccountID, adminUserID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000031"), acmeAccountID, aliceUserID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000032"), acmeAccountID, bobUserID, "auditor"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000033"), acmeTestAccountID, adminUserID, "owner"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000034"), acmeTestAccountID, aliceUserID, "admin"},
		{uuid.MustParse("00000000-0000-0000-0000-000000000035"), acmeTestAccountID, bobUserID, "auditor"},
	}
	repo := repositories.NewAccountUserRepository(nil)
	for _, m := range members {
		existing, err := repo.FindByID(ctx, m.id)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			return fmt.Errorf("find account user %s: %w", m.role, err)
		}
		if existing != nil && existing.ID != uuid.Nil {
			continue
		}
		if err := repo.Create(ctx, &models.AccountUser{
			ID:        m.id,
			AccountID: m.accountID,
			UserID:    m.userID,
			Role:      m.role,
			Status:    models.MembershipStatusActive,
		}); err != nil {
			return fmt.Errorf("create account_user %s: %w", m.role, err)
		}
		slog.Info("added user to account", "account_id", m.accountID, "role", m.role)
	}
	return nil
}
