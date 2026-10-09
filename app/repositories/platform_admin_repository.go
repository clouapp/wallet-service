package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// PlatformAdminRepository reads platform_admins. One row is one dashboard
// user. The user id is unique. This repository does not create admins.
type PlatformAdminRepository struct {
	db.Base
}

// NewPlatformAdminRepository wraps an orm.Query. Pass nil for a fresh query
// per call.
func NewPlatformAdminRepository(query orm.Query) *PlatformAdminRepository {
	return &PlatformAdminRepository{Base: db.NewBase(query)}
}

// Contains reports whether the user has a platform_admins row. A missing row
// is false. The lookup is not cached.
func (r *PlatformAdminRepository) Contains(ctx context.Context, userID uuid.UUID) (bool, error) {
	if userID == uuid.Nil {
		return false, fmt.Errorf("platform admin: user id is required")
	}

	rows := []models.PlatformAdmin{}
	err := r.Query(ctx).Raw(
		`SELECT user_id, created_at, updated_at
		 FROM platform_admins
		 WHERE user_id = ?`,
		userID,
	).Scan(&rows)
	if err != nil {
		return false, fmt.Errorf("platform admin lookup: %w", err)
	}
	return len(rows) > 0, nil
}
