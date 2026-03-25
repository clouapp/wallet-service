package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WithdrawalRepository interface {
	Create(w *models.Withdrawal) error
	FindByWallet(walletID uuid.UUID, status string, limit, offset int) ([]models.Withdrawal, int64, error)
	FindByIDAndWallet(withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error)
	UpdateStatus(id uuid.UUID, status string) error
}

type withdrawalRepository struct{}

func NewWithdrawalRepository() WithdrawalRepository {
	return &withdrawalRepository{}
}

func (r *withdrawalRepository) Create(w *models.Withdrawal) error {
	return facades.Orm().Query().Create(w)
}

func (r *withdrawalRepository) FindByWallet(walletID uuid.UUID, status string, limit, offset int) ([]models.Withdrawal, int64, error) {
	countQuery := facades.Orm().Query().
		Model(&models.Withdrawal{}).
		Where("wallet_id = ?", walletID)
	if status != "" {
		countQuery = countQuery.Where("status = ?", status)
	}
	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, err
	}

	dataQuery := facades.Orm().Query().
		Where("wallet_id = ?", walletID)
	if status != "" {
		dataQuery = dataQuery.Where("status = ?", status)
	}

	var withdrawals []models.Withdrawal
	err = dataQuery.Offset(offset).Limit(limit).Find(&withdrawals)
	return withdrawals, total, err
}

func (r *withdrawalRepository) FindByIDAndWallet(withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error) {
	var w models.Withdrawal
	err := facades.Orm().Query().
		Where("id = ? AND wallet_id = ?", withdrawalID, walletID).
		First(&w)
	if err != nil {
		return nil, err
	}
	if w.ID == uuid.Nil {
		return nil, nil
	}
	return &w, nil
}

func (r *withdrawalRepository) UpdateStatus(id uuid.UUID, status string) error {
	_, err := facades.Orm().Query().
		Model(&models.Withdrawal{}).
		Where("id = ?", id).
		Update("status", status)
	return err
}
