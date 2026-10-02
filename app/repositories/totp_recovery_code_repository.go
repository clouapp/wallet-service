package repositories

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type TotpRecoveryCodeRepository interface {
	FindUnusedByUserID(userID uuid.UUID) ([]models.TotpRecoveryCode, error)
	MarkUsedIfUnused(id uuid.UUID) (bool, error)
	CreateBatch(codes []models.TotpRecoveryCode) error
	DeleteByUserID(userID uuid.UUID) error
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

// MarkUsedIfUnused spends a recovery code. It reports false when the code was
// already spent, so two concurrent logins cannot both redeem the same code.
func (r *totpRecoveryCodeRepository) MarkUsedIfUnused(id uuid.UUID) (bool, error) {
	result, err := facades.Orm().Query().
		Model(&models.TotpRecoveryCode{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", time.Now())
	if err != nil {
		return false, err
	}
	return result.RowsAffected == 1, nil
}

func (r *totpRecoveryCodeRepository) CreateBatch(codes []models.TotpRecoveryCode) error {
	return facades.Orm().Query().Create(&codes)
}

func (r *totpRecoveryCodeRepository) DeleteByUserID(userID uuid.UUID) error {
	_, err := facades.Orm().Query().Where("user_id = ?", userID).Delete(&models.TotpRecoveryCode{})
	return err
}
