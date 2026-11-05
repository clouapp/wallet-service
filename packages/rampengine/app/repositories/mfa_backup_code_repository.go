package repositories

import (
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// MfaBackupCodeRepository is the persistence boundary for MFA backup codes. The
// codes are written and removed with their enrollment, by
// MfaCredentialRepository; this side reads them and spends one.
type MfaBackupCodeRepository interface {
	FindUnusedBySubject(subject identity.Subject) ([]models.MfaBackupCode, error)
	MarkUsedIfUnused(id uint) (bool, error)
}

type mfaBackupCodeRepository struct{}

// NewMfaBackupCodeRepository returns a repository backed by the default ORM connection.
func NewMfaBackupCodeRepository() MfaBackupCodeRepository {
	return &mfaBackupCodeRepository{}
}

func (r *mfaBackupCodeRepository) FindUnusedBySubject(subject identity.Subject) ([]models.MfaBackupCode, error) {
	var codes []models.MfaBackupCode
	if err := facades.Orm().Query().
		Where("subject_type", subject.SubjectType()).
		Where("subject_id", subject.SubjectID()).
		Where("used_at IS NULL").
		Find(&codes); err != nil {
		return nil, fmt.Errorf("mfa backup repo: find unused: %w", err)
	}
	return codes, nil
}

// MarkUsedIfUnused spends the code only while it is still unspent, and reports
// whether this call was the one that spent it.
func (r *mfaBackupCodeRepository) MarkUsedIfUnused(id uint) (bool, error) {
	now := time.Now()
	result, err := facades.Orm().Query().Model(&models.MfaBackupCode{}).
		Where("id", id).
		Where("used_at IS NULL").
		Update("used_at", now)
	if err != nil {
		return false, fmt.Errorf("mfa backup repo: mark used if unused: %w", err)
	}
	return result.RowsAffected == 1, nil
}
