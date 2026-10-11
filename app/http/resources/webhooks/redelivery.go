package webhooks

import "github.com/google/uuid"

// Redelivery is the answer of a redelivery request: the webhook and how many of
// its events were queued for delivery again.
type Redelivery struct {
	WebhookID uuid.UUID `json:"webhook_id"`
	Requeued  int       `json:"requeued"`
}

// NewRedelivery projects the outcome of a redelivery request.
func NewRedelivery(webhookID uuid.UUID, requeued int) Redelivery {
	return Redelivery{WebhookID: webhookID, Requeued: requeued}
}
