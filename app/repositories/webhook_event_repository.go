package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goravelerrors "github.com/goravel/framework/errors"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/types"
)

// WebhookEventRepository persists the webhook delivery queue.
type WebhookEventRepository struct {
	db.Base
}

// NewWebhookEventRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWebhookEventRepository(query orm.Query) *WebhookEventRepository {
	return &WebhookEventRepository{Base: db.NewBase(query)}
}

// Create inserts a delivery event.
func (r *WebhookEventRepository) Create(ctx context.Context, event *models.WebhookEvent) error {
	if event == nil {
		return fmt.Errorf("create webhook event: event is nil")
	}
	if err := r.Query(ctx).Create(event); err != nil {
		return fmt.Errorf("create webhook event: %w", err)
	}
	return nil
}

// AlreadyDelivered reports whether this event was already sent. A missing row
// is not delivered, so the worker can still post it. The check is the delivery
// state: a second SQS delivery of a delivered event must not post again.
func (r *WebhookEventRepository) AlreadyDelivered(ctx context.Context, eventID string) (bool, error) {
	id := strings.TrimSpace(eventID)
	if id == "" {
		return false, fmt.Errorf("webhook event id is required")
	}
	var event models.WebhookEvent
	err := r.Query(ctx).Where("id = ?", id).FirstOrFail(&event)
	if errors.Is(err, goravelerrors.OrmRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load webhook event: %w", err)
	}
	return event.DeliveryStatus == models.WebhookDeliveryDelivered, nil
}

// MarkDelivered sets delivery_status to delivered, records delivered_at, and
// increments attempts. A database error from the statement is ignored, matching
// the previous worker.
func (r *WebhookEventRepository) MarkDelivered(ctx context.Context, eventID string) error {
	now := time.Now().UTC()
	_, _ = r.Query(ctx).Exec(
		"UPDATE webhook_events SET delivery_status = 'delivered', delivered_at = ?, attempts = attempts + 1 WHERE id = ?",
		now, eventID,
	)
	return nil
}

// IncrementAttempt adds one attempt and stores last_error. A database error
// from the statement is ignored, matching the previous worker.
func (r *WebhookEventRepository) IncrementAttempt(ctx context.Context, eventID, errMsg string) error {
	_, _ = r.Query(ctx).Exec(
		"UPDATE webhook_events SET attempts = attempts + 1, last_error = ?, updated_at = (NOW() AT TIME ZONE 'UTC') WHERE id = ?",
		errMsg, eventID,
	)
	return nil
}

// ExistsForSubject reports whether this config already has an event for the subject.
func (r *WebhookEventRepository) ExistsForSubject(ctx context.Context, configID uuid.UUID, eventType, subjectID string) (bool, error) {
	if configID == uuid.Nil || eventType == "" || subjectID == "" {
		return false, fmt.Errorf("config id, event type and subject id are required")
	}
	count, err := r.Query(ctx).Model(&models.WebhookEvent{}).
		Where("webhook_config_id = ? AND event_type = ? AND subject_id = ?", configID, eventType, subjectID).
		Count()
	if err != nil {
		return false, fmt.Errorf("count webhook events: %w", err)
	}
	return count > 0, nil
}

// SupersededByDepositFailure reports whether eventID is a deposit.pending,
// deposit.confirming or deposit.confirmed event of a deposit that failed on chain:
// its transaction is failed, or a deposit.failed event is stored for that
// transaction (for any config: the tracker stores deposit.failed before it marks
// the transaction failed). An unknown event, or one without a transaction, is not.
func (r *WebhookEventRepository) SupersededByDepositFailure(ctx context.Context, eventID string) (bool, error) {
	id := strings.TrimSpace(eventID)
	if id == "" {
		return false, fmt.Errorf("webhook event id is required")
	}
	count, err := r.Query(ctx).Model(&models.WebhookEvent{}).
		Where("id = ? AND transaction_id IS NOT NULL", id).
		Where("event_type IN (?, ?, ?)", string(types.EventDepositPending), string(types.EventDepositConfirming), string(types.EventDepositConfirmed)).
		Where(`(EXISTS (
			SELECT 1 FROM transactions t
			WHERE t.id = webhook_events.transaction_id AND t.tx_type = ? AND t.status = ?
		) OR EXISTS (
			SELECT 1 FROM webhook_events f
			WHERE f.transaction_id = webhook_events.transaction_id AND f.event_type = ?
		))`, models.TxTypeDeposit, string(types.TxStatusFailed), string(types.EventDepositFailed)).
		Count()
	if err != nil {
		return false, fmt.Errorf("check webhook event superseded by a deposit failure: %w", err)
	}
	return count > 0, nil
}

// FindDueForDelivery returns undelivered events that know which config signs them,
// still have attempts left and whose exponential backoff (baseBackoff doubled per
// failed attempt, capped at maxBackoff) has elapsed, oldest first.
func (r *WebhookEventRepository) FindDueForDelivery(ctx context.Context, limit int, baseBackoff, maxBackoff time.Duration) ([]models.WebhookEvent, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	if baseBackoff <= 0 || maxBackoff < baseBackoff {
		return nil, fmt.Errorf("backoff must be positive and max backoff must not be below the base")
	}
	var events []models.WebhookEvent
	if err := r.Query(ctx).
		Where("delivery_status = ? AND webhook_config_id IS NOT NULL AND attempts < max_attempts", models.WebhookDeliveryPending).
		Where(
			"(attempts = 0 OR updated_at IS NULL OR updated_at <= (NOW() AT TIME ZONE 'UTC') - make_interval(secs => LEAST(? * power(2, attempts - 1), ?)))",
			baseBackoff.Seconds(), maxBackoff.Seconds(),
		).
		Order("created_at").
		Limit(limit).
		Find(&events); err != nil {
		return nil, fmt.Errorf("list due webhook events: %w", err)
	}
	return events, nil
}

// ClaimStaleForRedispatch takes up to limit pending events whose queue message was
// lost, oldest first: the event knows its config and that config is active, it has
// attempts left, and neither its insert nor any delivery attempt touched it for
// staleAfter. Each claimed row counts one attempt and is touched now, so the next
// run leaves it alone for another staleAfter and a row is claimed at most
// max_attempts times. Concurrent runs skip each other's rows (SKIP LOCKED).
func (r *WebhookEventRepository) ClaimStaleForRedispatch(ctx context.Context, staleAfter time.Duration, limit int) ([]models.WebhookEvent, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	if staleAfter <= 0 {
		return nil, fmt.Errorf("stale period must be positive")
	}
	var events []models.WebhookEvent
	if err := r.Query(ctx).Raw(`
		UPDATE webhook_events
		SET attempts = attempts + 1, updated_at = (NOW() AT TIME ZONE 'UTC')
		WHERE id IN (
			SELECT e.id FROM webhook_events e
			JOIN webhook_configs c ON c.id = e.webhook_config_id AND c.is_active
			WHERE e.delivery_status = ?
			  AND e.attempts < e.max_attempts
			  AND e.created_at <= (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?)
			  AND (e.updated_at IS NULL OR e.updated_at <= (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?))
			ORDER BY e.created_at
			LIMIT ?
			FOR UPDATE OF e SKIP LOCKED
		)
		RETURNING *`,
		models.WebhookDeliveryPending, staleAfter.Seconds(), staleAfter.Seconds(), limit,
	).Scan(&events); err != nil {
		return nil, fmt.Errorf("claim stale webhook events: %w", err)
	}
	return events, nil
}

// eventOfAccountWallet keeps a webhook_events row whose wallet belongs to the
// account bound to its placeholder. The wallet of an event is the wallet of its
// transaction; an event without one (wallet.gas_status.changed, a withdrawal event
// sent before its transaction existed) names it in data.wallet_id of its payload,
// which every such event carries. A row whose wallet cannot be told, or whose wallet
// has no account, is left out. A legacy webhook received the events of every account
// before an account claimed it, so this is what keeps a redelivery inside the
// caller's own wallets.
const eventOfAccountWallet = `EXISTS (
		SELECT 1 FROM wallets owned
		WHERE owned.account_id = ?
		  AND owned.id::text = COALESCE(
		      (SELECT t.wallet_id::text FROM transactions t WHERE t.id = webhook_events.transaction_id),
		      CASE WHEN webhook_events.transaction_id IS NULL
		           THEN webhook_events.payload::jsonb #>> '{data,wallet_id}' END
		  )
	)`

// FindForRedelivery returns the ids of configID's events about a wallet of
// accountID (eventOfAccountWallet) created in [since, until), of eventTypes when
// any are given, oldest first, at most limit. created_at holds UTC wall-clock time,
// so the bounds are converted to UTC in the query.
func (r *WebhookEventRepository) FindForRedelivery(ctx context.Context, configID, accountID uuid.UUID, since, until time.Time, eventTypes []string, limit int) ([]uuid.UUID, error) {
	if configID == uuid.Nil {
		return nil, fmt.Errorf("config id is required")
	}
	if accountID == uuid.Nil {
		return nil, fmt.Errorf("account id is required")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	query := r.Query(ctx).Model(&models.WebhookEvent{}).
		Where("webhook_config_id = ?", configID).
		Where("created_at >= (?::timestamptz AT TIME ZONE 'UTC') AND created_at < (?::timestamptz AT TIME ZONE 'UTC')", since, until).
		Where(eventOfAccountWallet, accountID)
	if len(eventTypes) > 0 {
		types := make([]any, len(eventTypes))
		for i, eventType := range eventTypes {
			types[i] = eventType
		}
		query = query.WhereIn("event_type", types)
	}
	var rows []struct{ ID uuid.UUID }
	if err := query.Select("id").Order("created_at").Order("id").Limit(limit).Scan(&rows); err != nil {
		return nil, fmt.Errorf("list webhook events to redeliver: %w", err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids, nil
}

// RequeueForRedelivery sets the listed events of configID about a wallet of
// accountID (eventOfAccountWallet) that were created at or after createdSince back
// to pending, with no attempts and no delivery time, and returns them. An event
// already pending that was touched after busySince (queued or attempted moments
// ago) is left as it is, so a repeated request does not queue it twice. An id of
// another config, of another account's wallet, or unknown, is skipped.
func (r *WebhookEventRepository) RequeueForRedelivery(ctx context.Context, configID, accountID uuid.UUID, ids []uuid.UUID, createdSince, busySince time.Time) ([]models.WebhookEvent, error) {
	if configID == uuid.Nil {
		return nil, fmt.Errorf("config id is required")
	}
	if accountID == uuid.Nil {
		return nil, fmt.Errorf("account id is required")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = id.String()
	}
	var events []models.WebhookEvent
	if err := r.Query(ctx).Raw(`
		UPDATE webhook_events
		SET delivery_status = ?, attempts = 0, delivered_at = NULL, updated_at = (NOW() AT TIME ZONE 'UTC')
		WHERE webhook_config_id = ?
		  AND id IN ?
		  AND created_at >= (?::timestamptz AT TIME ZONE 'UTC')
		  AND NOT (delivery_status = ? AND updated_at IS NOT NULL AND updated_at > (?::timestamptz AT TIME ZONE 'UTC'))
		  AND `+eventOfAccountWallet+`
		RETURNING *`,
		models.WebhookDeliveryPending, configID, values, createdSince, models.WebhookDeliveryPending, busySince, accountID,
	).Scan(&events); err != nil {
		return nil, fmt.Errorf("requeue webhook events: %w", err)
	}
	return events, nil
}

// MarkFailed sets delivery_status to failed and stores last_error.
func (r *WebhookEventRepository) MarkFailed(ctx context.Context, eventID, errMsg string) error {
	_, err := r.Query(ctx).Exec(
		"UPDATE webhook_events SET delivery_status = ?, last_error = ?, updated_at = (NOW() AT TIME ZONE 'UTC') WHERE id = ?",
		models.WebhookDeliveryFailed, errMsg, eventID,
	)
	if err != nil {
		return fmt.Errorf("mark webhook event failed: %w", err)
	}
	return nil
}
