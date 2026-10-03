package repositories

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	contractsorm "github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// SettingRepository persists settings rows. One row is one (account, group, key).
type SettingRepository struct {
	db.Base
}

// NewSettingRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewSettingRepository(query contractsorm.Query) *SettingRepository {
	return &SettingRepository{Base: db.NewBase(query)}
}

// ListGroup returns the stored rows of one account group. A missing group is
// an empty slice: the registry, not the table, decides which groups exist.
func (r *SettingRepository) ListGroup(ctx context.Context, accountID uuid.UUID, group string) ([]models.Setting, error) {
	if accountID == uuid.Nil {
		return nil, fmt.Errorf("list settings: account id is required")
	}
	group = strings.TrimSpace(group)
	if group == "" {
		return nil, fmt.Errorf("list settings: group is required")
	}

	rows := []models.Setting{}
	err := r.Query(ctx).Raw(
		`SELECT id, account_id, "group", "key", value, created_at, updated_at
		 FROM settings
		 WHERE account_id = ? AND "group" = ?`,
		accountID, group,
	).Scan(&rows)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	return rows, nil
}

// UpsertMany writes every value of one group in a single transaction.
// An empty map writes nothing. Callers seal secrets before they arrive here.
func (r *SettingRepository) UpsertMany(ctx context.Context, accountID uuid.UUID, group string, values map[string]string) error {
	if accountID == uuid.Nil {
		return fmt.Errorf("upsert settings: account id is required")
	}
	group = strings.TrimSpace(group)
	if group == "" {
		return fmt.Errorf("upsert settings: group is required")
	}
	if len(values) == 0 {
		return nil
	}

	err := r.Transaction(ctx, func(tx contractsorm.Query) error {
		writer := NewSettingRepository(tx)
		for key, value := range values {
			if err := writer.upsert(ctx, accountID, group, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("upsert settings: %w", err)
	}
	return nil
}

func (r *SettingRepository) upsert(ctx context.Context, accountID uuid.UUID, group, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("upsert settings: key is required")
	}
	_, err := r.Query(ctx).Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())
		 ON CONFLICT (account_id, "group", "key") WHERE account_id IS NOT NULL
		 DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		accountID, group, key, value,
	)
	if err != nil {
		return fmt.Errorf("upsert settings key %q: %w", key, err)
	}
	return nil
}
