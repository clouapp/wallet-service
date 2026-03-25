package repositories

import (
	"time"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type WebhookEventRepository interface {
	Create(event *models.WebhookEvent) error
	MarkDelivered(eventID string) error
	IncrementAttempt(eventID string, errMsg string) error
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
		"UPDATE webhook_events SET attempts = attempts + 1, last_error = ? WHERE id = ?",
		errMsg, eventID,
	)
	return nil
}
