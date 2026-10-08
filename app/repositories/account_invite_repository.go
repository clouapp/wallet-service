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

// Create inserts an invite. The statement keeps the caller's context so a
// caller-named event (member.invited) is what the audit plugin records. The
// token hash is on the row and off the allowlist, so the trail never reads it.
func (r *AccountInviteRepository) Create(ctx context.Context, invite *models.AccountInvite) error {
	if invite == nil {
		return fmt.Errorf("create account invite: invite is nil")
	}
	if err := r.statement(ctx).Create(invite); err != nil {
		return fmt.Errorf("create account invite: %w", err)
	}
	return nil
}

// FindPendingByAccountEmail returns the open invite for this email, or ErrRepositoryNotFound.
func (r *AccountInviteRepository) FindPendingByAccountEmail(ctx context.Context, accountID uuid.UUID, email string) (*models.AccountInvite, error) {
	var invite models.AccountInvite
	err := r.Query(ctx).
		Where("account_id = ? AND lower(email) = ? AND accepted_at IS NULL AND revoked_at IS NULL", accountID, strings.ToLower(strings.TrimSpace(email))).
		FirstOrFail(&invite)
	if err != nil {
		return nil, db.LookupError(err, "find pending invite")
	}
	return &invite, nil
}

// FindOpenByAccountAndID returns one invite that is not accepted and not
// revoked, or ErrRepositoryNotFound. An expired invite is still open: resend
// only replaces the token.
func (r *AccountInviteRepository) FindOpenByAccountAndID(ctx context.Context, accountID, id uuid.UUID) (*models.AccountInvite, error) {
	if accountID == uuid.Nil || id == uuid.Nil {
		return nil, fmt.Errorf("find open invite: account id and invite id are required")
	}
	var invite models.AccountInvite
	err := r.Query(ctx).
		Where("id = ? AND account_id = ? AND accepted_at IS NULL AND revoked_at IS NULL", id, accountID).
		FirstOrFail(&invite)
	if err != nil {
		return nil, db.LookupError(err, "find open invite")
	}
	return &invite, nil
}

// FindOpenByID returns one invite that is not accepted and not revoked, or
// ErrRepositoryNotFound. An expired invite is still open.
func (r *AccountInviteRepository) FindOpenByID(ctx context.Context, id uuid.UUID) (*models.AccountInvite, error) {
	if id == uuid.Nil {
		return nil, fmt.Errorf("find open invite: invite id is required")
	}
	var invite models.AccountInvite
	err := r.Query(ctx).
		Where("id = ? AND accepted_at IS NULL AND revoked_at IS NULL", id).
		FirstOrFail(&invite)
	if err != nil {
		return nil, db.LookupError(err, "find open invite")
	}
	return &invite, nil
}

// FindPendingByTokenHash returns an unexpired open invite, or ErrRepositoryNotFound.
func (r *AccountInviteRepository) FindPendingByTokenHash(ctx context.Context, tokenHash string, now time.Time) (*models.AccountInvite, error) {
	var invite models.AccountInvite
	err := r.Query(ctx).
		Where("token_hash = ? AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?", tokenHash, now).
		FirstOrFail(&invite)
	if err != nil {
		return nil, db.LookupError(err, "find invite by token")
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

// statement rebinds the open transaction to this call's context. Query returns
// the transaction as it was begun, which does not carry a WithIntent applied
// afterwards. The plugin reads that context for the caller-named event.
func (r *AccountInviteRepository) statement(ctx context.Context) orm.Query {
	query := r.Query(ctx)
	if ctx == nil {
		return query
	}
	contextual, ok := query.(orm.QueryWithContext)
	if !ok {
		return query
	}
	return contextual.WithContext(ctx)
}

const inviteListColumns = "id, account_id, email, role, invited_by, expires_at, accepted_at, revoked_at, created_at, updated_at"

// PaginateByAccountID pages an account's invites. The select list omits
// token_hash, and any value still on the struct is cleared before return.
func (r *AccountInviteRepository) PaginateByAccountID(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountInvite, int64, error) {
	if accountID == uuid.Nil {
		return nil, 0, fmt.Errorf("list account invites: account id is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list account invites: pagination bounds are invalid")
	}
	total, err := r.Query(ctx).Model(&models.AccountInvite{}).Where("account_id = ?", accountID).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count account invites: %w", err)
	}
	var invites []models.AccountInvite
	if err := r.Query(ctx).
		Select(inviteListColumns).
		Where("account_id = ?", accountID).
		Order("created_at DESC, id ASC").
		Offset(offset).
		Limit(limit).
		Find(&invites); err != nil {
		return nil, 0, fmt.Errorf("list account invites: %w", err)
	}
	for i := range invites {
		invites[i].TokenHash = ""
	}
	return invites, total, nil
}

// MarkAccepted stamps accepted_at.
func (r *AccountInviteRepository) MarkAccepted(ctx context.Context, id uuid.UUID, acceptedAt time.Time) error {
	if id == uuid.Nil {
		return fmt.Errorf("accept account invite: id is required")
	}
	if _, err := r.statement(ctx).Model(&models.AccountInvite{}).Where("id = ?", id).Update("accepted_at", acceptedAt); err != nil {
		return fmt.Errorf("accept account invite: %w", err)
	}
	return nil
}

// MarkRevoked stamps revoked_at on one open invite of this account. A row
// that is already accepted or revoked is left untouched. The statement keeps
// the caller's context so the audit plugin records the allowlisted columns.
// token_hash is not assigned.
func (r *AccountInviteRepository) MarkRevoked(ctx context.Context, accountID, id uuid.UUID, revokedAt time.Time) error {
	if ctx == nil {
		return fmt.Errorf("revoke account invite: context is required")
	}
	if accountID == uuid.Nil || id == uuid.Nil {
		return fmt.Errorf("revoke account invite: account id and invite id are required")
	}
	if revokedAt.IsZero() {
		return fmt.Errorf("revoke account invite: revoked_at is required")
	}
	result, err := r.statement(ctx).Model(&models.AccountInvite{}).
		Where("id = ? AND account_id = ? AND accepted_at IS NULL AND revoked_at IS NULL", id, accountID).
		Update("revoked_at", revokedAt)
	if err != nil {
		return fmt.Errorf("revoke account invite: %w", err)
	}
	if err := db.RequireRow(result); err != nil {
		return err
	}
	return nil
}
