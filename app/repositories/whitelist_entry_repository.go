package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// WhitelistEntryRepository persists withdrawal whitelist rows.
type WhitelistEntryRepository struct {
	db.Base
}

// NewWhitelistEntryRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWhitelistEntryRepository(query orm.Query) *WhitelistEntryRepository {
	return &WhitelistEntryRepository{Base: db.NewBase(query)}
}

// Create inserts a whitelist entry.
func (r *WhitelistEntryRepository) Create(ctx context.Context, entry *models.WhitelistEntry) error {
	if entry == nil {
		return fmt.Errorf("create whitelist entry: entry is nil")
	}
	if err := r.Query(ctx).Create(entry); err != nil {
		return fmt.Errorf("create whitelist entry: %w", err)
	}
	return nil
}

// FindByWalletID returns every entry for a wallet.
func (r *WhitelistEntryRepository) FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.WhitelistEntry, error) {
	var entries []models.WhitelistEntry
	if err := r.Query(ctx).Where("wallet_id = ?", walletID).Find(&entries); err != nil {
		return nil, fmt.Errorf("list whitelist entries: %w", err)
	}
	return entries, nil
}

// PaginateByWalletID pages through a wallet's whitelist.
func (r *WhitelistEntryRepository) PaginateByWalletID(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]models.WhitelistEntry, int64, error) {
	var entries []models.WhitelistEntry
	total, err := r.Query(ctx).Model(&models.WhitelistEntry{}).Where("wallet_id = ?", walletID).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count whitelist entries: %w", err)
	}
	if err := r.Query(ctx).Where("wallet_id = ?", walletID).Offset(offset).Limit(limit).Find(&entries); err != nil {
		return nil, 0, fmt.Errorf("list whitelist entries: %w", err)
	}
	return entries, total, nil
}

// FindByIDAndWallet returns the entry when it belongs to the wallet, or ErrRepositoryNotFound.
func (r *WhitelistEntryRepository) FindByIDAndWallet(ctx context.Context, id, walletID uuid.UUID) (*models.WhitelistEntry, error) {
	var entry models.WhitelistEntry
	if err := r.Query(ctx).Where("id = ? AND wallet_id = ?", id, walletID).First(&entry); err != nil {
		return nil, fmt.Errorf("find whitelist entry: %w", err)
	}
	if entry.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &entry, nil
}

// Delete removes a whitelist entry.
func (r *WhitelistEntryRepository) Delete(ctx context.Context, entry *models.WhitelistEntry) error {
	if entry == nil {
		return fmt.Errorf("delete whitelist entry: entry is nil")
	}
	if _, err := r.Query(ctx).Delete(entry); err != nil {
		return fmt.Errorf("delete whitelist entry: %w", err)
	}
	return nil
}
