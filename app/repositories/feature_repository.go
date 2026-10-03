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

// FeatureRepository persists account feature flags. One row is one
// (account, key) boolean. The catalog, not this repository, decides which
// keys exist.
type FeatureRepository struct {
	db.Base
}

// NewFeatureRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewFeatureRepository(query contractsorm.Query) *FeatureRepository {
	return &FeatureRepository{Base: db.NewBase(query)}
}

// ListAccount returns the stored flags of one account. No rows is an empty
// slice: a missing key is disabled, and this method does not insert one.
func (r *FeatureRepository) ListAccount(ctx context.Context, accountID uuid.UUID) ([]models.Feature, error) {
	if accountID == uuid.Nil {
		return nil, fmt.Errorf("list features: account id is required")
	}

	rows := []models.Feature{}
	err := r.Query(ctx).Raw(
		`SELECT id, account_id, "key", enabled, created_at, updated_at
		 FROM features
		 WHERE account_id = ?`,
		accountID,
	).Scan(&rows)
	if err != nil {
		return nil, fmt.Errorf("list features: %w", err)
	}
	return rows, nil
}

// Upsert writes one boolean. The caller has already rejected unknown keys.
func (r *FeatureRepository) Upsert(ctx context.Context, accountID uuid.UUID, key string, enabled bool) error {
	if accountID == uuid.Nil {
		return fmt.Errorf("upsert feature: account id is required")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("upsert feature: key is required")
	}

	_, err := r.Query(ctx).Exec(
		`INSERT INTO features (account_id, "key", enabled, created_at, updated_at)
		 VALUES (?, ?, ?, NOW(), NOW())
		 ON CONFLICT (account_id, "key")
		 DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = NOW()`,
		accountID, key, enabled,
	)
	if err != nil {
		return fmt.Errorf("upsert feature %q: %w", key, err)
	}
	return nil
}
