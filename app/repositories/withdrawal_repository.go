package repositories

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

type WithdrawalRepository interface {
	Create(w *models.Withdrawal) error
	FindByWallet(walletID uuid.UUID, status string, limit, offset int) ([]models.Withdrawal, int64, error)
	FindByIDAndWallet(withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error)
	FindByTransactionID(transactionID uuid.UUID) (*models.Withdrawal, error)
	FindBroadcastWithConfirmedTransaction(limit int) ([]models.Withdrawal, error)
	UpdateStatus(id uuid.UUID, status string) error
	UpdateFields(id uuid.UUID, fields map[string]any) error
}

type withdrawalRepository struct{}

func NewWithdrawalRepository() WithdrawalRepository {
	return &withdrawalRepository{}
}

func (r *withdrawalRepository) Create(w *models.Withdrawal) error {
	if w == nil {
		return fmt.Errorf("withdrawal is required")
	}
	if err := w.ValidateAmounts(); err != nil {
		return err
	}
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

func (r *withdrawalRepository) FindByTransactionID(transactionID uuid.UUID) (*models.Withdrawal, error) {
	if transactionID == uuid.Nil {
		return nil, fmt.Errorf("transaction id is required")
	}
	var w models.Withdrawal
	if err := facades.Orm().Query().Where("transaction_id = ?", transactionID).First(&w); err != nil {
		return nil, err
	}
	if w.ID == uuid.Nil {
		return nil, nil
	}
	return &w, nil
}

// FindBroadcastWithConfirmedTransaction returns withdrawals still marked broadcast whose
// on-chain transaction the confirmation tracker already confirmed, oldest first.
func (r *withdrawalRepository) FindBroadcastWithConfirmedTransaction(limit int) ([]models.Withdrawal, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	var withdrawals []models.Withdrawal
	err := facades.Orm().Query().
		Where(
			"status = ? AND transaction_id IN (SELECT id FROM transactions WHERE tx_type = ? AND status = ?)",
			models.WithdrawalStatusBroadcast, models.TxTypeWithdrawal, string(types.TxStatusConfirmed),
		).
		Order("created_at").
		Limit(limit).
		Find(&withdrawals)
	return withdrawals, err
}

func (r *withdrawalRepository) UpdateStatus(id uuid.UUID, status string) error {
	_, err := facades.Orm().Query().
		Model(&models.Withdrawal{}).
		Where("id = ?", id).
		Update("status", status)
	return err
}

func (r *withdrawalRepository) UpdateFields(id uuid.UUID, fields map[string]any) error {
	if id == uuid.Nil {
		return fmt.Errorf("withdrawal id is required")
	}
	if len(fields) == 0 {
		return fmt.Errorf("withdrawal update fields are required")
	}
	if err := amount.RequireNonNegativeColumns(fields, models.WithdrawalAmountColumns...); err != nil {
		return err
	}
	_, err := facades.Orm().Query().
		Model(&models.Withdrawal{}).
		Where("id = ?", id).
		Update(fields)
	return err
}
