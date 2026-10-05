package seeds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
)

// SeedUsers inserts dashboard users with deterministic IDs.
func SeedUsers(ctx context.Context) error {
	users := []struct {
		id       uuid.UUID
		email    string
		password string
		fullName string
	}{
		{adminUserID, "admin@macro.markets", "secret", "Admin User"},
		{aliceUserID, "alice@macro.markets", "secret", "Alice Smith"},
		{bobUserID, "bob@macro.markets", "secret", "Bob Jones"},
	}

	repo := repositories.NewUserRepository(nil)
	for _, u := range users {
		existing, err := repo.FindByID(ctx, u.id)
		if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
			return fmt.Errorf("find user %s: %w", u.email, err)
		}
		if existing != nil && existing.ID != uuid.Nil {
			slog.Info("user already exists, ensuring default account", "email", u.email)
			if existing.DefaultAccountID == nil || *existing.DefaultAccountID != acmeAccountID {
				accountID := acmeAccountID
				if err := repo.UpdateDefaultAccountID(ctx, u.id, &accountID); err != nil {
					return fmt.Errorf("update user default account %s: %w", u.email, err)
				}
			}
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(u.password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		defAcc := acmeAccountID
		// Preferences stay nil so UserRepository.Create omits the column and
		// the jsonb default '{}' applies.
		if err := repo.Create(ctx, &models.User{
			ID:               u.id,
			Email:            u.email,
			PasswordHash:     string(hash),
			FullName:         u.fullName,
			Status:           "active",
			DefaultAccountID: &defAcc,
		}); err != nil {
			return fmt.Errorf("create user %s: %w", u.email, err)
		}
		slog.Info("created user", "email", u.email)
	}
	return nil
}
