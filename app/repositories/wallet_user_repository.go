package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// WalletUserRepository persists wallet memberships.
// FindByWalletAndUser returns an active membership only. FindByWalletID and
// IncludeDeleted return every status for membership management.
type WalletUserRepository struct {
	db.Base
}

// NewWalletUserRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWalletUserRepository(query orm.Query) *WalletUserRepository {
	return &WalletUserRepository{Base: db.NewBase(query)}
}

// Create inserts a wallet membership.
func (r *WalletUserRepository) Create(ctx context.Context, wu *models.WalletUser) error {
	if wu == nil {
		return fmt.Errorf("create wallet user: membership is nil")
	}
	if err := r.Query(ctx).Create(wu); err != nil {
		return fmt.Errorf("create wallet user: %w", err)
	}
	return nil
}

// FindByID returns the membership row, including one that is not active, or ErrRepositoryNotFound.
func (r *WalletUserRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.WalletUser, error) {
	var wu models.WalletUser
	if err := r.Query(ctx).Where("id = ?", id).FirstOrFail(&wu); err != nil {
		return nil, db.LookupError(err, "find wallet user by id")
	}
	return &wu, nil
}

// FindByWalletID returns active memberships for a wallet.
func (r *WalletUserRepository) FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.WalletUser, error) {
	var members []models.WalletUser
	if err := r.Query(ctx).Where("wallet_id = ? AND deleted_at IS NULL", walletID).Find(&members); err != nil {
		return nil, fmt.Errorf("list wallet users: %w", err)
	}
	return members, nil
}

// FindByWalletAndUser returns the active membership, or ErrRepositoryNotFound.
func (r *WalletUserRepository) FindByWalletAndUser(ctx context.Context, walletID, userID uuid.UUID) (*models.WalletUser, error) {
	var wu models.WalletUser
	if err := r.Query(ctx).Where("wallet_id = ? AND user_id = ? AND deleted_at IS NULL AND status = ?", walletID, userID, models.StatusActive).FirstOrFail(&wu); err != nil {
		return nil, db.LookupError(err, "find wallet user")
	}
	return &wu, nil
}

// FindByWalletAndUserIncludeDeleted returns the membership including a soft-deleted row, or ErrRepositoryNotFound.
func (r *WalletUserRepository) FindByWalletAndUserIncludeDeleted(ctx context.Context, walletID, userID uuid.UUID) (*models.WalletUser, error) {
	var wu models.WalletUser
	if err := r.Query(ctx).Where("wallet_id = ? AND user_id = ?", walletID, userID).FirstOrFail(&wu); err != nil {
		return nil, db.LookupError(err, "find wallet user including deleted")
	}
	return &wu, nil
}

// Restore clears deleted_at on a wallet membership.
func (r *WalletUserRepository) Restore(ctx context.Context, id uuid.UUID) error {
	if _, err := r.Query(ctx).Model(&models.WalletUser{}).Where("id = ?", id).Update("deleted_at", nil); err != nil {
		return fmt.Errorf("restore wallet user: %w", err)
	}
	return nil
}

// SetRoles sets wallet_users.roles.
func (r *WalletUserRepository) SetRoles(ctx context.Context, id uuid.UUID, roles string) error {
	if _, err := r.Query(ctx).Model(&models.WalletUser{}).Where("id = ?", id).Update("roles", roles); err != nil {
		return fmt.Errorf("set wallet user roles: %w", err)
	}
	return nil
}

// SoftDelete sets deleted_at on the active membership.
func (r *WalletUserRepository) SoftDelete(ctx context.Context, walletID, userID uuid.UUID) error {
	now := time.Now()
	if _, err := r.Query(ctx).Model(&models.WalletUser{}).
		Where("wallet_id = ? AND user_id = ? AND deleted_at IS NULL", walletID, userID).
		Update("deleted_at", now); err != nil {
		return fmt.Errorf("soft delete wallet user: %w", err)
	}
	return nil
}
