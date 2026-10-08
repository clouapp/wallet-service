package repositories

import (
	"context"
	"fmt"

	contractsorm "github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// ActivityLogRepository deletes aged activity_log rows. Inserts stay in
// packages/activitylog, which must not import this module.
type ActivityLogRepository struct {
	db.Base
}

// NewActivityLogRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewActivityLogRepository(query contractsorm.Query) *ActivityLogRepository {
	return &ActivityLogRepository{Base: db.NewBase(query)}
}

// DeleteOlderThan removes one batch of activity_log rows older than days.
func (r *ActivityLogRepository) DeleteOlderThan(ctx context.Context, days, limit int) (int64, error) {
	if r == nil {
		return 0, fmt.Errorf("prune activity_log: repository is required")
	}
	result, err := r.Query(ctx).Exec(
		`DELETE FROM activity_log WHERE id IN (
			SELECT id FROM activity_log WHERE created_at < NOW() - (? * INTERVAL '1 day')
			ORDER BY created_at LIMIT ?
		)`,
		days, limit,
	)
	if err != nil {
		return 0, fmt.Errorf("prune activity_log: %w", err)
	}
	return result.RowsAffected, nil
}
