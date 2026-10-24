package repositories

import (
	"context"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// DepositSignatureCursorRepository persists where the deposit signature sync of each
// account of a chain resumes. The sync only moves a cursor forward (Advance).
type DepositSignatureCursorRepository struct {
	db.Base
}

// NewDepositSignatureCursorRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewDepositSignatureCursorRepository(query orm.Query) *DepositSignatureCursorRepository {
	return &DepositSignatureCursorRepository{Base: db.NewBase(query)}
}

// FindByChain returns the cursor of every account of the chain, none for a chain
// never synced. A stored negative slot is an error.
func (r *DepositSignatureCursorRepository) FindByChain(ctx context.Context, chainID string) ([]models.DepositSignatureCursor, error) {
	var rows []models.DepositSignatureCursor
	if err := r.Query(ctx).Where("chain", chainID).Order("account").Find(&rows); err != nil {
		return nil, fmt.Errorf("find deposit signature cursors: %w", err)
	}
	for _, row := range rows {
		if row.Slot < 0 || row.SyncedFromSlot < 0 {
			return nil, fmt.Errorf("deposit signature cursor of %s %s holds a negative slot", row.Chain, row.Account)
		}
	}
	return rows, nil
}

// Advance stores cursor, creating the account's row on its first call. An existing
// row moves to the cursor's signature and slot only when that slot is not behind
// the stored one, so a slower sync never rewinds an account, and it keeps the slot
// its history was first read from. A blank chain or account, or a negative slot, is
// refused.
func (r *DepositSignatureCursorRepository) Advance(ctx context.Context, cursor models.DepositSignatureCursor) error {
	if strings.TrimSpace(cursor.Chain) == "" || strings.TrimSpace(cursor.Account) == "" {
		return fmt.Errorf("advance deposit signature cursor: chain and account are required")
	}
	if cursor.Slot < 0 || cursor.SyncedFromSlot < 0 {
		return fmt.Errorf("advance deposit signature cursor: slots must not be negative")
	}
	if _, err := r.Query(ctx).Exec(
		`INSERT INTO deposit_signature_cursors (chain, account, signature, slot, synced_from_slot, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())
		 ON CONFLICT (chain, account) DO UPDATE
		 SET signature = EXCLUDED.signature, slot = EXCLUDED.slot, updated_at = NOW()
		 WHERE deposit_signature_cursors.slot <= EXCLUDED.slot`,
		cursor.Chain, cursor.Account, cursor.Signature, cursor.Slot, cursor.SyncedFromSlot,
	); err != nil {
		return fmt.Errorf("advance deposit signature cursor: %w", err)
	}
	return nil
}
