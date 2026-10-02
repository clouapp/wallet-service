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

// TotpRecoveryCodeRepository persists one-time TOTP recovery codes.
type TotpRecoveryCodeRepository struct {
	db.Base
}

// NewTotpRecoveryCodeRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewTotpRecoveryCodeRepository(query orm.Query) *TotpRecoveryCodeRepository {
	return &TotpRecoveryCodeRepository{Base: db.NewBase(query)}
}

// FindUnusedByUserID returns recovery codes that have not been used.
func (r *TotpRecoveryCodeRepository) FindUnusedByUserID(ctx context.Context, userID uuid.UUID) ([]models.TotpRecoveryCode, error) {
	var codes []models.TotpRecoveryCode
	if err := r.Query(ctx).Where("user_id = ? AND used_at IS NULL", userID).Find(&codes); err != nil {
		return nil, fmt.Errorf("list unused recovery codes: %w", err)
	}
	return codes, nil
}

// MarkUsed sets used_at on a recovery code.
func (r *TotpRecoveryCodeRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	if _, err := r.Query(ctx).Model(&models.TotpRecoveryCode{}).Where("id = ?", id).Update("used_at", now); err != nil {
		return fmt.Errorf("mark recovery code used: %w", err)
	}
	return nil
}

// CreateBatch inserts recovery codes.
func (r *TotpRecoveryCodeRepository) CreateBatch(ctx context.Context, codes []models.TotpRecoveryCode) error {
	if err := r.Query(ctx).Create(&codes); err != nil {
		return fmt.Errorf("create recovery codes: %w", err)
	}
	return nil
}

// DeleteByUserID removes every recovery code for the user.
func (r *TotpRecoveryCodeRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	if _, err := r.Query(ctx).Where("user_id = ?", userID).Delete(&models.TotpRecoveryCode{}); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	return nil
}
