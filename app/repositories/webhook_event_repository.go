package repositories

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WebhookEventRepository interface {
	Create(event *models.WebhookEvent) error
	MarkDelivered(eventID string) error
	IncrementAttempt(eventID string, errMsg string) error
	ExistsForSubject(configID uuid.UUID, eventType, subjectID string) (bool, error)
	FindDueForDelivery(limit int, baseBackoff, maxBackoff time.Duration) ([]models.WebhookEvent, error)
	MarkFailed(eventID string, errMsg string) error
}

type webhookEventRepository struct{}

func NewWebhookEventRepository() WebhookEventRepository {
	return &webhookEventRepository{}
}

func (r *webhookEventRepository) Create(event *models.WebhookEvent) error {
	return facades.Orm().Query().Create(event)
}

func (r *webhookEventRepository) MarkDelivered(eventID string) error {
	now := time.Now().UTC()
	facades.Orm().Query().Exec(
		"UPDATE webhook_events SET delivery_status = 'delivered', delivered_at = ?, attempts = attempts + 1 WHERE id = ?",
		now, eventID,
	)
	return nil
}

func (r *webhookEventRepository) IncrementAttempt(eventID string, errMsg string) error {
	facades.Orm().Query().Exec(
		"UPDATE webhook_events SET attempts = attempts + 1, last_error = ?, updated_at = (NOW() AT TIME ZONE 'UTC') WHERE id = ?",
		errMsg, eventID,
	)
	return nil
}

func (r *webhookEventRepository) ExistsForSubject(configID uuid.UUID, eventType, subjectID string) (bool, error) {
	if configID == uuid.Nil || eventType == "" || subjectID == "" {
		return false, fmt.Errorf("config id, event type and subject id are required")
	}
	count, err := facades.Orm().Query().
		Model(&models.WebhookEvent{}).
		Where("webhook_config_id = ? AND event_type = ? AND subject_id = ?", configID, eventType, subjectID).
		Count()
	return count > 0, err
}

// FindDueForDelivery returns undelivered events that know which config signs them,
// still have attempts left and whose exponential backoff (baseBackoff doubled per
// failed attempt, capped at maxBackoff) has elapsed, oldest first.
func (r *webhookEventRepository) FindDueForDelivery(limit int, baseBackoff, maxBackoff time.Duration) ([]models.WebhookEvent, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	if baseBackoff <= 0 || maxBackoff < baseBackoff {
		return nil, fmt.Errorf("backoff must be positive and max backoff must not be below the base")
	}
	var events []models.WebhookEvent
	err := facades.Orm().Query().
		Where("delivery_status = ? AND webhook_config_id IS NOT NULL AND attempts < max_attempts", models.WebhookDeliveryPending).
		Where(
			"(attempts = 0 OR updated_at IS NULL OR updated_at <= (NOW() AT TIME ZONE 'UTC') - make_interval(secs => LEAST(? * power(2, attempts - 1), ?)))",
			baseBackoff.Seconds(), maxBackoff.Seconds(),
		).
		Order("created_at").
		Limit(limit).
		Find(&events)
	return events, err
}

func (r *webhookEventRepository) MarkFailed(eventID string, errMsg string) error {
	_, err := facades.Orm().Query().Exec(
		"UPDATE webhook_events SET delivery_status = ?, last_error = ?, updated_at = (NOW() AT TIME ZONE 'UTC') WHERE id = ?",
		models.WebhookDeliveryFailed, errMsg, eventID,
	)
	return err
}
