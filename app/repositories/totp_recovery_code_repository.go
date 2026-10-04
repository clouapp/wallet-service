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
// Codes live in mfa_backup_codes. Rows still on totp_recovery_codes are
// included until the copy removes them.
func (r *TotpRecoveryCodeRepository) FindUnusedByUserID(ctx context.Context, userID uuid.UUID) ([]models.TotpRecoveryCode, error) {
	var shared []models.MfaBackupCode
	if err := r.Query(ctx).Where("subject_type = ? AND subject_id = ? AND used_at IS NULL", models.MFASubjectUsers, userID).Find(&shared); err != nil {
		return nil, fmt.Errorf("list unused recovery codes: %w", err)
	}
	codes := make([]models.TotpRecoveryCode, 0, len(shared))
	seen := make(map[uuid.UUID]struct{}, len(shared))
	for _, code := range shared {
		seen[code.ID] = struct{}{}
		codes = append(codes, models.TotpRecoveryCode{
			ID:       code.ID,
			UserID:   code.SubjectID,
			CodeHash: code.CodeHash,
			UsedAt:   code.UsedAt,
		})
	}
	var legacy []models.TotpRecoveryCode
	if err := r.Query(ctx).Where("user_id = ? AND used_at IS NULL", userID).Find(&legacy); err != nil {
		return nil, fmt.Errorf("list unused recovery codes: %w", err)
	}
	for _, code := range legacy {
		if _, ok := seen[code.ID]; ok {
			continue
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// MarkUsed sets used_at on a recovery code.
func (r *TotpRecoveryCodeRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	if _, err := r.Query(ctx).Model(&models.MfaBackupCode{}).Where("id = ?", id).Update("used_at", now); err != nil {
		return fmt.Errorf("mark recovery code used: %w", err)
	}
	if _, err := r.Query(ctx).Model(&models.TotpRecoveryCode{}).Where("id = ?", id).Update("used_at", now); err != nil {
		return fmt.Errorf("mark recovery code used: %w", err)
	}
	return nil
}

// MarkUsedIfUnused spends a recovery code. It reports false when the code was
// already spent, so two concurrent logins cannot both redeem the same code.
func (r *TotpRecoveryCodeRepository) MarkUsedIfUnused(ctx context.Context, id uuid.UUID) (bool, error) {
	now := time.Now()
	result, err := r.Query(ctx).Model(&models.MfaBackupCode{}).Where("id = ? AND used_at IS NULL", id).Update("used_at", now)
	if err != nil {
		return false, fmt.Errorf("mark recovery code used: %w", err)
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	result, err = r.Query(ctx).Model(&models.TotpRecoveryCode{}).Where("id = ? AND used_at IS NULL", id).Update("used_at", now)
	if err != nil {
		return false, fmt.Errorf("mark recovery code used: %w", err)
	}
	return result.RowsAffected == 1, nil
}

// CreateBatch inserts recovery codes into mfa_backup_codes for the user subject.
func (r *TotpRecoveryCodeRepository) CreateBatch(ctx context.Context, codes []models.TotpRecoveryCode) error {
	if len(codes) == 0 {
		return nil
	}
	rows := make([]models.MfaBackupCode, 0, len(codes))
	for _, code := range codes {
		if code.ID == uuid.Nil || code.UserID == uuid.Nil || code.CodeHash == "" {
			return fmt.Errorf("create recovery codes: id, user and hash are required")
		}
		rows = append(rows, models.MfaBackupCode{
			ID:          code.ID,
			SubjectType: models.MFASubjectUsers,
			SubjectID:   code.UserID,
			CodeHash:    code.CodeHash,
		})
	}
	if err := r.Query(ctx).Create(&rows); err != nil {
		return fmt.Errorf("create recovery codes: %w", err)
	}
	return nil
}

// CountByUserID reports how many recovery rows the user still has. The
// hashes stay in the table.
func (r *TotpRecoveryCodeRepository) CountByUserID(ctx context.Context, userID uuid.UUID) (int64, error) {
	if userID == uuid.Nil {
		return 0, fmt.Errorf("count recovery codes: user id is required")
	}
	shared, err := r.Query(ctx).Model(&models.MfaBackupCode{}).Where("subject_type = ? AND subject_id = ?", models.MFASubjectUsers, userID).Count()
	if err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	legacy, err := r.Query(ctx).Model(&models.TotpRecoveryCode{}).Where("user_id = ?", userID).Count()
	if err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	total := shared + legacy
	if total < 0 {
		return 0, fmt.Errorf("count recovery codes: count is negative")
	}
	return total, nil
}

// DeleteByUserID removes every recovery code for the user.
func (r *TotpRecoveryCodeRepository) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	if _, err := r.Query(ctx).Where("subject_type = ? AND subject_id = ?", models.MFASubjectUsers, userID).Delete(&models.MfaBackupCode{}); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	if _, err := r.Query(ctx).Where("user_id = ?", userID).Delete(&models.TotpRecoveryCode{}); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	return nil
}
