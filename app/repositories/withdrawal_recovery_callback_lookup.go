package repositories

import (
	"context"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// FindByOriginalRecoveryTransactionID identifies a predecessor whose ordinary
// idempotency key was released before recovery. Returning the current aggregate
// lets the publisher suppress a late predecessor event, including an old row
// that no longer has an idempotency key. Legacy orphan transactions stay legacy.
func (r *WithdrawalRepository) FindByOriginalRecoveryTransactionID(ctx context.Context, transactionID uuid.UUID) (*models.Withdrawal, error) {
	var withdrawal models.Withdrawal
	err := r.Query(ctx).Where("id IN (SELECT operation_id FROM withdrawal_recovery_attempts WHERE original_transaction_id = ?)", transactionID).FirstOrFail(&withdrawal)
	if err != nil {
		return nil, db.LookupError(err, "find withdrawal by recovery predecessor")
	}
	return &withdrawal, nil
}
