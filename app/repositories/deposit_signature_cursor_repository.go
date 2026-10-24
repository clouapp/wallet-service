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
		if row.Slot < 0 || row.SyncedFromSlot < 0 || row.AheadSlot < 0 {
			return nil, fmt.Errorf("deposit signature cursor of %s %s holds a negative slot", row.Chain, row.Account)
		}
	}
	return rows, nil
}

// Advance stores cursor, creating the account's row on its first call. An existing
// row moves to the cursor's signature, slot, backfill position and its anchor, end
// sighting and stretch ahead of a gap (with its anchor) only when that slot is not
// behind the stored one, so a
// slower sync never rewinds an account; it keeps the floor its history was first read
// down to, and once covered it stays covered. A blank chain or account, or a negative
// slot, is refused.
func (r *DepositSignatureCursorRepository) Advance(ctx context.Context, cursor models.DepositSignatureCursor) error {
	if strings.TrimSpace(cursor.Chain) == "" || strings.TrimSpace(cursor.Account) == "" {
		return fmt.Errorf("advance deposit signature cursor: chain and account are required")
	}
	if cursor.Slot < 0 || cursor.SyncedFromSlot < 0 || cursor.AheadSlot < 0 {
		return fmt.Errorf("advance deposit signature cursor: slots must not be negative")
	}
	if _, err := r.Query(ctx).Exec(
		`INSERT INTO deposit_signature_cursors (chain, account, signature, slot, backfill_before, backfill_anchor, synced_from_slot,
		     covered, end_seen, ahead_signature, ahead_slot, ahead_before, ahead_anchor, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())
		 ON CONFLICT (chain, account) DO UPDATE
		 SET signature = EXCLUDED.signature, slot = EXCLUDED.slot, backfill_before = EXCLUDED.backfill_before,
		     backfill_anchor = EXCLUDED.backfill_anchor,
		     covered = deposit_signature_cursors.covered OR EXCLUDED.covered, end_seen = EXCLUDED.end_seen,
		     ahead_signature = EXCLUDED.ahead_signature, ahead_slot = EXCLUDED.ahead_slot, ahead_before = EXCLUDED.ahead_before,
		     ahead_anchor = EXCLUDED.ahead_anchor, updated_at = NOW()
		 WHERE deposit_signature_cursors.slot <= EXCLUDED.slot`,
		cursor.Chain, cursor.Account, cursor.Signature, cursor.Slot, cursor.BackfillBefore, cursor.BackfillAnchor, cursor.SyncedFromSlot,
		cursor.Covered, cursor.EndSeen, cursor.AheadSignature, cursor.AheadSlot, cursor.AheadBefore, cursor.AheadAnchor,
	); err != nil {
		return fmt.Errorf("advance deposit signature cursor: %w", err)
	}
	return nil
}
