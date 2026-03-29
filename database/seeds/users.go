package seeds

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"golang.org/x/crypto/bcrypt"

	"github.com/macrowallets/waas/app/models"
)

// SeedUsers inserts dashboard users with deterministic IDs.
func SeedUsers(_ context.Context) error {
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

	for _, u := range users {
		var existing models.User
		if err := facades.Orm().Query().Where("id", u.id).First(&existing); err == nil && existing.ID != uuid.Nil {
			slog.Info("user already exists, ensuring default account", "email", u.email)
			if existing.DefaultAccountID == nil || *existing.DefaultAccountID != acmeAccountID {
				if _, err := facades.Orm().Query().Model(&models.User{}).Where("id = ?", u.id).Update("default_account_id", acmeAccountID); err != nil {
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
		user := models.User{
			ID:               u.id,
			Email:            u.email,
			PasswordHash:     string(hash),
			FullName:         u.fullName,
			Status:           "active",
			DefaultAccountID: &defAcc,
		}
		if err := facades.Orm().Query().Create(&user); err != nil {
			return fmt.Errorf("create user %s: %w", u.email, err)
		}
		slog.Info("created user", "email", u.email)
	}
	return nil
}
