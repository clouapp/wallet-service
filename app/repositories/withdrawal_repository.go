package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

// WithdrawalRepository loads and updates withdrawal rows.
type WithdrawalRepository struct {
	db.Base
}

// NewWithdrawalRepository builds a repository. Nil uses a fresh query per call.
func NewWithdrawalRepository(query orm.Query) *WithdrawalRepository {
	return &WithdrawalRepository{Base: db.NewBase(query)}
}

// Within runs fn inside one transaction. Queries made with the callback
// context join that transaction.
func (r *WithdrawalRepository) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("withdrawal transaction: callback is required")
	}
	return r.Transaction(ctx, func(tx orm.Query) error {
		return fn(db.WithTx(ctx, tx))
	})
}

// Create inserts a withdrawal after rejecting a negative amount or fee estimate.
func (r *WithdrawalRepository) Create(ctx context.Context, w *models.Withdrawal) error {
	if w == nil {
		return fmt.Errorf("withdrawal is required")
	}
	if err := w.ValidateAmounts(); err != nil {
		return err
	}
	if err := r.Query(ctx).Create(w); err != nil {
		return fmt.Errorf("create withdrawal: %w", err)
	}
	return nil
}

// FindByWallet lists withdrawals for a wallet, optionally filtered by status.
func (r *WithdrawalRepository) FindByWallet(ctx context.Context, walletID uuid.UUID, status string, limit, offset int) ([]models.Withdrawal, int64, error) {
	countQuery := r.Query(ctx).Model(&models.Withdrawal{}).Where("wallet_id = ?", walletID)
	if status != "" {
		countQuery = countQuery.Where("status = ?", status)
	}
	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count withdrawals: %w", err)
	}

	dataQuery := r.Query(ctx).Where("wallet_id = ?", walletID)
	if status != "" {
		dataQuery = dataQuery.Where("status = ?", status)
	}
	var withdrawals []models.Withdrawal
	if err := dataQuery.Offset(offset).Limit(limit).Find(&withdrawals); err != nil {
		return nil, 0, fmt.Errorf("list withdrawals: %w", err)
	}
	return withdrawals, total, nil
}

// FindByID returns the withdrawal, or ErrRepositoryNotFound.
func (r *WithdrawalRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Withdrawal, error) {
	if id == uuid.Nil {
		return nil, fmt.Errorf("withdrawal id is required")
	}
	var w models.Withdrawal
	if err := r.Query(ctx).Where("id = ?", id).FirstOrFail(&w); err != nil {
		return nil, db.LookupError(err, "find withdrawal")
	}
	return &w, nil
}

// FindByIDAndWallet returns the withdrawal when it belongs to walletID.
func (r *WithdrawalRepository) FindByIDAndWallet(ctx context.Context, withdrawalID, walletID uuid.UUID) (*models.Withdrawal, error) {
	var w models.Withdrawal
	if err := r.Query(ctx).Where("id = ? AND wallet_id = ?", withdrawalID, walletID).FirstOrFail(&w); err != nil {
		return nil, db.LookupError(err, "find withdrawal")
	}
	return &w, nil
}

// FindByTransactionID returns the withdrawal linked to a transaction.
func (r *WithdrawalRepository) FindByTransactionID(ctx context.Context, transactionID uuid.UUID) (*models.Withdrawal, error) {
	if transactionID == uuid.Nil {
		return nil, fmt.Errorf("transaction id is required")
	}
	var w models.Withdrawal
	if err := r.Query(ctx).Where("transaction_id = ?", transactionID).FirstOrFail(&w); err != nil {
		return nil, db.LookupError(err, "find withdrawal by transaction")
	}
	return &w, nil
}

// FindBroadcastWithConfirmedTransaction returns withdrawals still marked broadcast
// whose on-chain transaction the confirmation tracker already confirmed, oldest first.
func (r *WithdrawalRepository) FindBroadcastWithConfirmedTransaction(ctx context.Context, limit int) ([]models.Withdrawal, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	var withdrawals []models.Withdrawal
	err := r.Query(ctx).
		Where(
			"status = ? AND transaction_id IN (SELECT id FROM transactions WHERE tx_type = ? AND status = ?)",
			models.WithdrawalStatusBroadcast, models.TxTypeWithdrawal, string(types.TxStatusConfirmed),
		).
		Order("created_at").
		Limit(limit).
		Find(&withdrawals)
	if err != nil {
		return nil, fmt.Errorf("find broadcast withdrawals: %w", err)
	}
	return withdrawals, nil
}

// SetStatus sets withdrawals.status.
func (r *WithdrawalRepository) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	return r.updateColumns(ctx, id, map[string]any{"status": status}, "set withdrawal status")
}

// RetryBroadcast moves a failed withdrawal back to broadcasting and clears the failure.
func (r *WithdrawalRepository) RetryBroadcast(ctx context.Context, id uuid.UUID, amount, destination, feeEstimate, note string) error {
	return r.updateColumns(ctx, id, map[string]any{
		"status":              "broadcasting",
		"failure_reason":      nil,
		"amount":              amount,
		"destination_address": destination,
		"fee_estimate":        feeEstimate,
		"note":                note,
	}, "retry withdrawal broadcast")
}

// MarkFailed records a terminal failure and its reason.
func (r *WithdrawalRepository) MarkFailed(ctx context.Context, id uuid.UUID, failureReason string) error {
	return r.updateColumns(ctx, id, map[string]any{
		"status":         models.WithdrawalStatusFailed,
		"failure_reason": failureReason,
	}, "mark withdrawal failed")
}

// MarkBroadcast stores the broadcast status and the transaction that carries it.
func (r *WithdrawalRepository) MarkBroadcast(ctx context.Context, id uuid.UUID, transactionID *uuid.UUID) error {
	return r.updateColumns(ctx, id, map[string]any{
		"status":         "broadcast",
		"transaction_id": transactionID,
	}, "mark withdrawal broadcast")
}

// MarkConfirmed stores the confirmed status and the transaction that settled it.
func (r *WithdrawalRepository) MarkConfirmed(ctx context.Context, id uuid.UUID, transactionID uuid.UUID) error {
	return r.updateColumns(ctx, id, map[string]any{
		"status":         models.WithdrawalStatusConfirmed,
		"transaction_id": transactionID,
	}, "mark withdrawal confirmed")
}

// SetFeeEstimate sets withdrawals.fee_estimate.
func (r *WithdrawalRepository) SetFeeEstimate(ctx context.Context, id uuid.UUID, feeEstimate string) error {
	return r.updateColumns(ctx, id, map[string]any{"fee_estimate": feeEstimate}, "set withdrawal fee estimate")
}

func (r *WithdrawalRepository) updateColumns(ctx context.Context, id uuid.UUID, columns map[string]any, op string) error {
	if id == uuid.Nil {
		return fmt.Errorf("withdrawal id is required")
	}
	if len(columns) == 0 {
		return fmt.Errorf("withdrawal update columns are required")
	}
	if err := amount.RequireNonNegativeColumns(columns, models.WithdrawalAmountColumns...); err != nil {
		return err
	}
	if _, err := r.Query(ctx).Model(&models.Withdrawal{}).Where("id = ?", id).Update(columns); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
