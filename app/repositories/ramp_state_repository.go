package repositories

import (
	"context"
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"time"
)

type RampState struct{ db.Base }

func NewRampState() *RampState { return &RampState{Base: db.NewBase(nil)} }

// FundingIntentAbsent is used only under the ramp funding lock for a terminal
// order. Any native row, even failed, requires its own settlement evidence.
func (r *RampState) FundingIntentAbsent(ctx context.Context, orderID uuid.UUID) (bool, error) {
	var result struct{ Absent bool }
	err := r.Query(ctx).Raw(`SELECT NOT EXISTS(SELECT 1 FROM withdrawals WHERE id=?)
	 AND NOT EXISTS(SELECT 1 FROM transactions WHERE idempotency_key=?) AS absent`, orderID, orderID.String()).Scan(&result)
	return result.Absent, err
}

func (r *RampState) Enqueue(ctx context.Context, accountID uuid.UUID, kind, payload string, delay time.Duration) error {
	var account any
	if accountID != uuid.Nil {
		account = accountID
	}
	_, err := r.Query(ctx).Exec("INSERT INTO ramp_tasks(id,account_id,kind,payload,available_at) VALUES(?,?,?,?::jsonb,?) ON CONFLICT DO NOTHING", uuid.New(), account, kind, payload, time.Now().Add(delay))
	return err
}
func (r *RampState) Claim(ctx context.Context, limit int) ([]models.RampTask, error) {
	var rows []models.RampTask
	err := r.Query(ctx).Raw(`WITH due AS (
 SELECT id FROM ramp_tasks WHERE completed_at IS NULL AND attempts < 12 AND available_at <= now()
 AND (locked_until IS NULL OR locked_until < now()) ORDER BY available_at FOR UPDATE SKIP LOCKED LIMIT ?
 ) UPDATE ramp_tasks t SET locked_until=now()+interval '2 minutes',attempts=t.attempts+1 FROM due WHERE t.id=due.id RETURNING t.*`, limit).Scan(&rows)
	return rows, err
}
func (r *RampState) Finish(ctx context.Context, id uuid.UUID, cause error) error {
	if cause == nil {
		_, err := r.Query(ctx).Exec("UPDATE ramp_tasks SET completed_at=now(),locked_until=NULL,last_error=NULL WHERE id=?", id)
		return err
	}
	// Provider responses may contain PII. Detailed outcomes stay in the encrypted engine timeline.
	_, err := r.Query(ctx).Exec("UPDATE ramp_tasks SET locked_until=NULL,available_at=now()+LEAST(interval '30 minutes',interval '10 seconds'*power(2,attempts)),last_error=? WHERE id=?", "ramp task failed; inspect the holder's provider timeline", id)
	return err
}
func (r *RampState) AppendOutbox(ctx context.Context, id, accountID, customerID uuid.UUID, kind, payload string) error {
	_, err := r.Query(ctx).Exec("INSERT INTO ramp_outbox_events(id,account_id,customer_id,event_type,payload) VALUES(?,?,?,?,?::jsonb) ON CONFLICT(id) DO NOTHING", id, accountID, customerID, kind, payload)
	return err
}
func (r *RampState) ListOutbox(ctx context.Context, limit int) ([]models.RampOutboxEvent, error) {
	var rows []models.RampOutboxEvent
	err := r.Query(ctx).Table("ramp_outbox_events").WhereNull("delivered_at").OrderBy("created_at").Limit(limit).Get(&rows)
	return rows, err
}
func (r *RampState) DeliverOutbox(ctx context.Context, id uuid.UUID) error {
	_, err := r.Query(ctx).Exec("UPDATE ramp_outbox_events SET delivered_at=now(),attempts=attempts+1 WHERE id=?", id)
	return err
}
func (r *RampState) ReconciliationCustomers(ctx context.Context, limit int) ([]models.RampReconciliationCustomer, error) {
	var rows []models.RampReconciliationCustomer
	err := r.Query(ctx).Raw("SELECT c.id,c.account_id,c.engine_customer_id,a.environment FROM ramp_customers c JOIN accounts a ON a.id=c.account_id ORDER BY c.updated_at LIMIT ?", limit).Scan(&rows)
	return rows, err
}
func (r *RampState) TouchCustomer(ctx context.Context, id uuid.UUID) error {
	_, err := r.Query(ctx).Table("ramp_customers").Where("id", id).Update("updated_at", time.Now())
	return err
}
