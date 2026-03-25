package repositories

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type TotpRecoveryCodeRepository interface {
	FindUnusedByUserID(userID uuid.UUID) ([]models.TotpRecoveryCode, error)
	MarkUsed(id uuid.UUID) error
}

type totpRecoveryCodeRepository struct{}

func NewTotpRecoveryCodeRepository() TotpRecoveryCodeRepository {
	return &totpRecoveryCodeRepository{}
}

func (r *totpRecoveryCodeRepository) FindUnusedByUserID(userID uuid.UUID) ([]models.TotpRecoveryCode, error) {
	var codes []models.TotpRecoveryCode
	err := facades.Orm().Query().
		Where("user_id = ? AND used_at IS NULL", userID).
		Find(&codes)
	return codes, err
}

func (r *totpRecoveryCodeRepository) MarkUsed(id uuid.UUID) error {
	now := time.Now()
	_, err := facades.Orm().Query().
		Model(&models.TotpRecoveryCode{}).
		Where("id = ?", id).
		Update("used_at", now)
	return err
}
