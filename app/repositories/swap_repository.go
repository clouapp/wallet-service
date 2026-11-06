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

// SwapRepository stores immutable quotes and guarded order transitions.
type SwapRepository struct{ db.Base }

// NewSwapRepository binds the store to a query, or resolves one per call when nil.
func NewSwapRepository(query orm.Query) *SwapRepository {
	return &SwapRepository{Base: db.NewBase(query)}
}

// CreateQuote persists one quote before it is offered to an integrator.
func (r *SwapRepository) CreateQuote(ctx context.Context, quote *models.SwapQuote) error {
	if err := r.Query(ctx).Create(quote); err != nil {
		return fmt.Errorf("create swap quote: %w", err)
	}
	return nil
}

// FindQuote returns an account's quote from its source wallet.
func (r *SwapRepository) FindQuote(ctx context.Context, accountID, walletID, id uuid.UUID) (*models.SwapQuote, error) {
	var row models.SwapQuote
	err := r.Query(ctx).Where("id = ? AND account_id = ? AND source_wallet_id = ?", id, accountID, walletID).FirstOrFail(&row)
	if err != nil {
		return nil, db.LookupError(err, "find swap quote")
	}
	return &row, nil
}

// CreateOrder atomically claims a quote and an account-scoped idempotency key.
func (r *SwapRepository) CreateOrder(ctx context.Context, order *models.SwapOrder) error {
	if err := r.Query(ctx).Create(order); err != nil {
		return fmt.Errorf("create swap order: %w", err)
	}
	return nil
}

// FindOrder returns an order only inside its account and source wallet.
func (r *SwapRepository) FindOrder(ctx context.Context, accountID, walletID, id uuid.UUID) (*models.SwapOrder, error) {
	var row models.SwapOrder
	err := r.Query(ctx).Where("id = ? AND account_id = ? AND source_wallet_id = ?", id, accountID, walletID).FirstOrFail(&row)
	if err != nil {
		return nil, db.LookupError(err, "find swap order")
	}
	return &row, nil
}

// FindOrderByKey resolves retries without issuing another provider request.
func (r *SwapRepository) FindOrderByKey(ctx context.Context, accountID, key uuid.UUID) (*models.SwapOrder, error) {
	var row models.SwapOrder
	err := r.Query(ctx).Where("account_id = ? AND idempotency_key = ?", accountID, key).FirstOrFail(&row)
	if err != nil {
		return nil, db.LookupError(err, "find swap order by key")
	}
	return &row, nil
}

// ListOrders returns the newest account-scoped orders from a wallet.
func (r *SwapRepository) ListOrders(ctx context.Context, accountID, walletID uuid.UUID, limit, offset int) ([]models.SwapOrder, int64, error) {
	query := r.Query(ctx).Model(&models.SwapOrder{}).Where("account_id = ? AND source_wallet_id = ?", accountID, walletID)
	total, err := query.Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count swap orders: %w", err)
	}
	var rows []models.SwapOrder
	err = r.Query(ctx).Where("account_id = ? AND source_wallet_id = ?", accountID, walletID).Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list swap orders: %w", err)
	}
	return rows, total, nil
}

// ListOpenOrders returns accepted, non-terminal orders, the least recently
// checked first, for the background reconciler.
func (r *SwapRepository) ListOpenOrders(ctx context.Context, limit int) ([]models.SwapOrder, error) {
	var rows []models.SwapOrder
	err := r.Query(ctx).WhereIn("status", []any{"preparing", "broadcast", "processing", "reconciling", "refund_pending"}).
		Order("last_checked_at ASC NULLS FIRST").Order("created_at ASC").Limit(limit).Find(&rows)
	if err != nil {
		return nil, fmt.Errorf("list open swap orders: %w", err)
	}
	return rows, nil
}

// SaveFunding persists the pinned funding instruction before anything is signed.
func (r *SwapRepository) SaveFunding(ctx context.Context, id uuid.UUID, payload string) error {
	result, err := r.Query(ctx).Model(&models.SwapOrder{}).Where("id = ? AND status = ? AND (funding_payload = '{}' OR funding_payload = ?)", id, "preparing", payload).Update(map[string]any{"funding_payload": payload})
	if err != nil {
		return fmt.Errorf("save swap funding: %w", err)
	}
	return db.RequireRow(result)
}

// RecordSource records a send without changing an already terminal order.
func (r *SwapRepository) RecordSource(ctx context.Context, id uuid.UUID, hash, status string) error {
	result, err := r.Query(ctx).Model(&models.SwapOrder{}).Where("id = ? AND status IN (?, ?, ?) AND (source_tx_hash = '' OR source_tx_hash = ?)", id, "preparing", "broadcast", "reconciling", hash).Update(map[string]any{"source_tx_hash": hash, "status": status})
	if err != nil {
		return fmt.Errorf("record swap source: %w", err)
	}
	return db.RequireRow(result)
}

// RecordProgress guards a tracking update against concurrent terminal transitions.
func (r *SwapRepository) RecordProgress(ctx context.Context, row *models.SwapOrder, previous string) error {
	result, err := r.Query(ctx).Model(&models.SwapOrder{}).Where("id = ? AND status = ?", row.ID, previous).Update(map[string]any{
		"status": row.Status, "destination_tx_hash": row.DestinationTxHash, "received_raw": row.ReceivedRaw,
		"refund_tx_hash": row.RefundTxHash, "refund_raw": row.RefundRaw, "failure_code": row.FailureCode, "last_checked_at": time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("record swap progress: %w", err)
	}
	return db.RequireRow(result)
}
