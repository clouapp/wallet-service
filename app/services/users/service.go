package users

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// Store is the user persistence dashboard handlers still performed themselves.
type Store interface {
	FindByEmail(ctx context.Context, email string) (*models.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.User, error)
	Create(ctx context.Context, user *models.User) error
	UpdateDefaultAccountID(ctx context.Context, id uuid.UUID, defaultAccountID *uuid.UUID) error
	UpdateFullName(ctx context.Context, id uuid.UUID, fullName string) error
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error
	UpdatePreferences(ctx context.Context, id uuid.UUID, prefs *models.UserPreferences) error
	UpdateTotpSecret(ctx context.Context, id uuid.UUID, secret string) error
	EnableTotp(ctx context.Context, id uuid.UUID) error
	DisableTotp(ctx context.Context, id uuid.UUID) error
}

// Service is the user reads and writes the dashboard handlers call.
type Service struct {
	store Store
}

// NewService builds a user service.
func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) require(ctx context.Context, op string) error {
	if ctx == nil {
		return fmt.Errorf("%s: context is required", op)
	}
	if s == nil || s.store == nil {
		return fmt.Errorf("users service: users repository is required")
	}
	return nil
}

// FindByEmail returns the user for email.
func (s *Service) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	if err := s.require(ctx, "find user by email"); err != nil {
		return nil, err
	}
	return s.store.FindByEmail(ctx, email)
}

// FindByID returns the user for id.
func (s *Service) FindByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	if err := s.require(ctx, "find user"); err != nil {
		return nil, err
	}
	return s.store.FindByID(ctx, id)
}

// Create inserts a user the caller already filled in.
func (s *Service) Create(ctx context.Context, user *models.User) error {
	if err := s.require(ctx, "create user"); err != nil {
		return err
	}
	if user == nil {
		return fmt.Errorf("create user: user is required")
	}
	return s.store.Create(ctx, user)
}

// UpdateDefaultAccountID sets the user's default account.
func (s *Service) UpdateDefaultAccountID(ctx context.Context, id uuid.UUID, defaultAccountID *uuid.UUID) error {
	if err := s.require(ctx, "update default account"); err != nil {
		return err
	}
	return s.store.UpdateDefaultAccountID(ctx, id, defaultAccountID)
}

// UpdateFullName sets the user's full name.
func (s *Service) UpdateFullName(ctx context.Context, id uuid.UUID, fullName string) error {
	if err := s.require(ctx, "update full name"); err != nil {
		return err
	}
	return s.store.UpdateFullName(ctx, id, fullName)
}

// UpdatePasswordHash sets the user's password hash.
func (s *Service) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	if err := s.require(ctx, "update password"); err != nil {
		return err
	}
	return s.store.UpdatePasswordHash(ctx, id, hash)
}

// UpdatePreferences replaces the user's preference document.
func (s *Service) UpdatePreferences(ctx context.Context, id uuid.UUID, prefs *models.UserPreferences) error {
	if err := s.require(ctx, "update preferences"); err != nil {
		return err
	}
	return s.store.UpdatePreferences(ctx, id, prefs)
}

// UpdateTotpSecret stores the encrypted TOTP secret.
func (s *Service) UpdateTotpSecret(ctx context.Context, id uuid.UUID, secret string) error {
	if err := s.require(ctx, "update totp secret"); err != nil {
		return err
	}
	return s.store.UpdateTotpSecret(ctx, id, secret)
}

// EnableTotp marks TOTP as enabled.
func (s *Service) EnableTotp(ctx context.Context, id uuid.UUID) error {
	if err := s.require(ctx, "enable totp"); err != nil {
		return err
	}
	return s.store.EnableTotp(ctx, id)
}

// DisableTotp marks TOTP as disabled.
func (s *Service) DisableTotp(ctx context.Context, id uuid.UUID) error {
	if err := s.require(ctx, "disable totp"); err != nil {
		return err
	}
	return s.store.DisableTotp(ctx, id)
}
