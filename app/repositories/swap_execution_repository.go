package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

func (r *SwapRepository) Within(ctx context.Context, fn func(context.Context) error) error {
	return r.Transaction(ctx, func(tx orm.Query) error { return fn(db.WithTx(ctx, tx)) })
}

// ExecutionSteps lists every signed transaction in its original execution order.
func (r *SwapRepository) ExecutionSteps(ctx context.Context, orderID uuid.UUID) ([]models.SwapExecutionStep, error) {
	var rows []models.SwapExecutionStep
	if err := r.Query(ctx).Where("order_id", orderID).Order("position").Find(&rows); err != nil {
		return nil, fmt.Errorf("list swap execution steps: %w", err)
	}
	return rows, nil
}

// RecordSigned atomically records a signed transaction and its order linkage.
// The source hash is durable before the network can receive the funding bytes.
func (r *SwapRepository) RecordSigned(ctx context.Context, step *models.SwapExecutionStep, tx *models.Transaction) error {
	return r.Within(ctx, func(txCtx context.Context) error {
		if err := r.Query(txCtx).Create(tx); err != nil {
			return fmt.Errorf("record swap transaction: %w", err)
		}
		if err := r.Query(txCtx).Create(step); err != nil {
			return fmt.Errorf("record swap execution step: %w", err)
		}
		if step.Purpose == "funding" {
			return r.RecordSource(txCtx, step.OrderID, step.TxHash, "reconciling")
		}
		return nil
	})
}
