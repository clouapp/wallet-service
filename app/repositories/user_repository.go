package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// UserRepository persists users. Preferences live on the users row, not in a
// separate table.
type UserRepository struct {
	db.Base
}

// NewUserRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewUserRepository(query orm.Query) *UserRepository {
	return &UserRepository{Base: db.NewBase(query)}
}

// FindByEmail returns the user with this email, or ErrRepositoryNotFound.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User
	if err := r.Query(ctx).Where("email = ?", email).First(&user); err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	if user.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &user, nil
}

// FindByID returns the user with this id, or ErrRepositoryNotFound.
func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var user models.User
	if err := r.Query(ctx).Where("id = ?", id).First(&user); err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	if user.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &user, nil
}

// Create inserts a user. A nil Preferences pointer is omitted so the column
// default '{}' applies; users.preferences is NOT NULL.
func (r *UserRepository) Create(ctx context.Context, user *models.User) error {
	if user == nil {
		return fmt.Errorf("create user: user is nil")
	}
	query := r.Query(ctx)
	if user.Preferences != nil {
		if err := query.Create(user); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		return nil
	}
	if err := query.Omit("Preferences").Create(user); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	user.Preferences = &models.UserPreferences{}
	return nil
}

// UpdateDefaultAccountID sets users.default_account_id.
func (r *UserRepository) UpdateDefaultAccountID(ctx context.Context, id uuid.UUID, defaultAccountID *uuid.UUID) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("default_account_id", defaultAccountID); err != nil {
		return fmt.Errorf("update user default account: %w", err)
	}
	return nil
}

// UpdateFullName sets users.full_name.
func (r *UserRepository) UpdateFullName(ctx context.Context, id uuid.UUID, fullName string) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("full_name", fullName); err != nil {
		return fmt.Errorf("update user full name: %w", err)
	}
	return nil
}

// UpdatePasswordHash sets users.password_hash.
func (r *UserRepository) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("password_hash", hash); err != nil {
		return fmt.Errorf("update user password hash: %w", err)
	}
	return nil
}

// UpdatePreferences sets users.preferences.
func (r *UserRepository) UpdatePreferences(ctx context.Context, id uuid.UUID, prefs *models.UserPreferences) error {
	jsonBytes, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("update user preferences: %w", err)
	}
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("preferences", string(jsonBytes)); err != nil {
		return fmt.Errorf("update user preferences: %w", err)
	}
	return nil
}

// UpdateTotpSecret sets users.totp_secret.
func (r *UserRepository) UpdateTotpSecret(ctx context.Context, id uuid.UUID, secret string) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("totp_secret", secret); err != nil {
		return fmt.Errorf("update user totp secret: %w", err)
	}
	return nil
}

// EnableTotp sets users.totp_enabled.
func (r *UserRepository) EnableTotp(ctx context.Context, id uuid.UUID) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update("totp_enabled", true); err != nil {
		return fmt.Errorf("enable user totp: %w", err)
	}
	return nil
}

// DisableTotp clears totp_enabled and totp_secret together.
func (r *UserRepository) DisableTotp(ctx context.Context, id uuid.UUID) error {
	if _, err := r.Query(ctx).Model(&models.User{}).Where("id = ?", id).Update(map[string]any{
		"totp_enabled": false,
		"totp_secret":  "",
	}); err != nil {
		return fmt.Errorf("disable user totp: %w", err)
	}
	return nil
}
