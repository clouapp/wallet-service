package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// AccountInviteRepository persists hashed invite tokens.
type AccountInviteRepository struct {
	db.Base
}

// NewAccountInviteRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewAccountInviteRepository(query orm.Query) *AccountInviteRepository {
	return &AccountInviteRepository{Base: db.NewBase(query)}
}

// Create inserts an invite.
func (r *AccountInviteRepository) Create(ctx context.Context, invite *models.AccountInvite) error {
	if invite == nil {
		return fmt.Errorf("create account invite: invite is nil")
	}
	if err := r.Query(ctx).Create(invite); err != nil {
		return fmt.Errorf("create account invite: %w", err)
	}
	return nil
}

// FindPendingByAccountEmail returns the open invite for this email, or ErrRepositoryNotFound.
func (r *AccountInviteRepository) FindPendingByAccountEmail(ctx context.Context, accountID uuid.UUID, email string) (*models.AccountInvite, error) {
	var invite models.AccountInvite
	err := r.Query(ctx).
		Where("account_id = ? AND lower(email) = ? AND accepted_at IS NULL AND revoked_at IS NULL", accountID, strings.ToLower(strings.TrimSpace(email))).
		First(&invite)
	if err != nil {
		return nil, fmt.Errorf("find pending invite: %w", err)
	}
	if invite.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &invite, nil
}

// FindPendingByTokenHash returns an unexpired open invite, or ErrRepositoryNotFound.
func (r *AccountInviteRepository) FindPendingByTokenHash(ctx context.Context, tokenHash string, now time.Time) (*models.AccountInvite, error) {
	var invite models.AccountInvite
	err := r.Query(ctx).
		Where("token_hash = ? AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?", tokenHash, now).
		First(&invite)
	if err != nil {
		return nil, fmt.Errorf("find invite by token: %w", err)
	}
	if invite.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &invite, nil
}

// Rotate replaces the hash, role, and expiry of one invite.
func (r *AccountInviteRepository) Rotate(ctx context.Context, id uuid.UUID, tokenHash, role string, expiresAt time.Time) error {
	if id == uuid.Nil {
		return fmt.Errorf("rotate account invite: id is required")
	}
	_, err := r.Query(ctx).Exec(
		`UPDATE account_invites SET token_hash = ?, role = ?, expires_at = ?, updated_at = NOW() WHERE id = ?`,
		tokenHash, role, expiresAt, id,
	)
	if err != nil {
		return fmt.Errorf("rotate account invite: %w", err)
	}
	return nil
}

// MarkAccepted stamps accepted_at.
func (r *AccountInviteRepository) MarkAccepted(ctx context.Context, id uuid.UUID, acceptedAt time.Time) error {
	if id == uuid.Nil {
		return fmt.Errorf("accept account invite: id is required")
	}
	if _, err := r.Query(ctx).Model(&models.AccountInvite{}).Where("id = ?", id).Update("accepted_at", acceptedAt); err != nil {
		return fmt.Errorf("accept account invite: %w", err)
	}
	return nil
}
