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

// ListPlatform returns the stored rows of one platform group (account_id NULL).
// A missing group is an empty slice.
func (r *SettingRepository) ListPlatform(ctx context.Context, group string) ([]models.Setting, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return nil, fmt.Errorf("list platform settings: group is required")
	}

	rows := []models.Setting{}
	err := r.Query(ctx).Raw(
		`SELECT id, account_id, "group", "key", value, created_at, updated_at
		 FROM settings
		 WHERE account_id IS NULL AND "group" = ?`,
		group,
	).Scan(&rows)
	if err != nil {
		return nil, fmt.Errorf("list platform settings: %w", err)
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

// UpsertPlatform writes every value of one platform group (account_id NULL)
// in a single transaction. An empty map writes nothing. Callers do not pass
// secrets here: webhook_delivery stores attempts and a timeout only.
func (r *SettingRepository) UpsertPlatform(ctx context.Context, group string, values map[string]string) error {
	group = strings.TrimSpace(group)
	if group == "" {
		return fmt.Errorf("upsert platform settings: group is required")
	}
	if len(values) == 0 {
		return nil
	}

	err := r.Transaction(ctx, func(tx contractsorm.Query) error {
		writer := NewSettingRepository(tx)
		for key, value := range values {
			if err := writer.upsertPlatform(ctx, group, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("upsert platform settings: %w", err)
	}
	return nil
}

func (r *SettingRepository) upsertPlatform(ctx context.Context, group, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("upsert platform settings: key is required")
	}
	_, err := r.Query(ctx).Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, ?, ?, ?, NOW(), NOW())
		 ON CONFLICT ("group", "key") WHERE account_id IS NULL
		 DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		group, key, value,
	)
	if err != nil {
		return fmt.Errorf("upsert platform settings key %q: %w", key, err)
	}
	return nil
}

// DeletePlatform removes every stored row of one platform group (account_id
// NULL). An account row that happens to use the same group name is left in
// place. Deleting a group that has no rows is success: the registry default
// is already in force.
func (r *SettingRepository) DeletePlatform(ctx context.Context, group string) error {
	group = strings.TrimSpace(group)
	if group == "" {
		return fmt.Errorf("delete platform settings: group is required")
	}
	_, err := r.Query(ctx).Exec(
		`DELETE FROM settings WHERE account_id IS NULL AND "group" = ?`,
		group,
	)
	if err != nil {
		return fmt.Errorf("delete platform settings: %w", err)
	}
	return nil
}

// DeleteGroup removes every stored row of one account group. A platform row
// (account_id NULL) and another account's rows are left in place. Deleting a
// group that has no rows is success: the registry default is already in force.
func (r *SettingRepository) DeleteGroup(ctx context.Context, accountID uuid.UUID, group string) error {
	if accountID == uuid.Nil {
		return fmt.Errorf("delete settings: account id is required")
	}
	group = strings.TrimSpace(group)
	if group == "" {
		return fmt.Errorf("delete settings: group is required")
	}
	_, err := r.Query(ctx).Exec(
		`DELETE FROM settings WHERE account_id = ? AND "group" = ?`,
		accountID, group,
	)
	if err != nil {
		return fmt.Errorf("delete settings: %w", err)
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
