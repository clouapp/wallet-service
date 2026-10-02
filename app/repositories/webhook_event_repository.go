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
