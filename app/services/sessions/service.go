package sessions

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func requireStore(ctx context.Context, store any, op, what string) error {
	if ctx == nil {
		return fmt.Errorf("%s: context is required", op)
	}
	if store == nil {
		return fmt.Errorf("%s: %s repository is required", op, what)
	}
	return nil
}

// RefreshStore is the refresh-token persistence.
type RefreshStore interface {
	Create(ctx context.Context, token *models.RefreshToken) error
	FindValidTokens(ctx context.Context) ([]models.RefreshToken, error)
	RevokeByID(ctx context.Context, id uuid.UUID) error
	RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error)
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// RefreshTokens reads and writes refresh tokens.
type RefreshTokens struct{ store RefreshStore }

// NewRefreshTokens builds the refresh-token service.
func NewRefreshTokens(store RefreshStore) *RefreshTokens { return &RefreshTokens{store: store} }

func (s *RefreshTokens) storeOrNil() any {
	if s == nil {
		return nil
	}
	return s.store
}

func (s *RefreshTokens) Create(ctx context.Context, token *models.RefreshToken) error {
	if err := requireStore(ctx, s.storeOrNil(), "create refresh token", "refresh tokens"); err != nil {
		return err
	}
	return s.store.Create(ctx, token)
}

func (s *RefreshTokens) FindValidTokens(ctx context.Context) ([]models.RefreshToken, error) {
	if err := requireStore(ctx, s.storeOrNil(), "list refresh tokens", "refresh tokens"); err != nil {
		return nil, err
	}
	return s.store.FindValidTokens(ctx)
}

func (s *RefreshTokens) RevokeByID(ctx context.Context, id uuid.UUID) error {
	if err := requireStore(ctx, s.storeOrNil(), "revoke refresh token", "refresh tokens"); err != nil {
		return err
	}
	return s.store.RevokeByID(ctx, id)
}

func (s *RefreshTokens) RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error) {
	if err := requireStore(ctx, s.storeOrNil(), "revoke refresh token", "refresh tokens"); err != nil {
		return false, err
	}
	return s.store.RevokeIfActive(ctx, id)
}

func (s *RefreshTokens) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	if err := requireStore(ctx, s.storeOrNil(), "revoke refresh tokens", "refresh tokens"); err != nil {
		return err
	}
	return s.store.RevokeAllForUser(ctx, userID)
}

// ResetStore is the password-reset token persistence.
type ResetStore interface {
	Create(ctx context.Context, token *models.PasswordResetToken) error
	FindValidTokens(ctx context.Context) ([]models.PasswordResetToken, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
}

// PasswordResets reads and writes password-reset tokens.
type PasswordResets struct{ store ResetStore }

// NewPasswordResets builds the password-reset service.
func NewPasswordResets(store ResetStore) *PasswordResets { return &PasswordResets{store: store} }

func (s *PasswordResets) storeOrNil() any {
	if s == nil {
		return nil
	}
	return s.store
}

func (s *PasswordResets) Create(ctx context.Context, token *models.PasswordResetToken) error {
	if err := requireStore(ctx, s.storeOrNil(), "create password reset", "password resets"); err != nil {
		return err
	}
	return s.store.Create(ctx, token)
}

func (s *PasswordResets) FindValidTokens(ctx context.Context) ([]models.PasswordResetToken, error) {
	if err := requireStore(ctx, s.storeOrNil(), "list password resets", "password resets"); err != nil {
		return nil, err
	}
	return s.store.FindValidTokens(ctx)
}

func (s *PasswordResets) MarkUsed(ctx context.Context, id uuid.UUID) error {
	if err := requireStore(ctx, s.storeOrNil(), "mark password reset used", "password resets"); err != nil {
		return err
	}
	return s.store.MarkUsed(ctx, id)
}
