package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// RedispatchStaleAfter is how long a pending outbox row stays untouched, since
	// its insert and since its last delivery attempt, before its queue message
	// counts as lost. The queue retries a failing message every visibility timeout
	// (60 s), so a row the worker still retries is never this old.
	RedispatchStaleAfter = 5 * time.Minute
	// RedispatchBatchSize bounds the rows one re-dispatch run sends to the queue.
	RedispatchBatchSize = 50
)

// RedispatchStale sends to the webhook queue again the outbox rows whose queue
// message was lost: the send after the insert failed, or the message left the
// queue without a delivery and without a recorded attempt. It runs only where a
// webhook queue delivers; without one the local outbox delivers the rows itself.
// Each re-dispatch counts as one attempt, so a row is sent at most max_attempts
// times, and a row just re-dispatched waits RedispatchStaleAfter before the next
// one. A send that fails leaves the row for a later run. It returns how many rows
// reached the queue.
func (s *Service) RedispatchStale(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, errors.New("redispatch webhooks: limit must be positive")
	}
	if s.sqs == nil {
		return 0, errors.New("redispatch webhooks: no queue sender configured")
	}
	events, err := s.webhookEventRepo.ClaimStaleForRedispatch(ctx, RedispatchStaleAfter, limit)
	if err != nil {
		return 0, fmt.Errorf("redispatch webhooks: %w", err)
	}
	sent := 0
	for _, event := range events {
		if err := s.sqs.SendWebhook(ctx, queuedMessage(event, event.Attempts)); err != nil {
			slog.Error("redispatch webhook: sqs send failed", "error", err, "event_id", event.ID)
			continue
		}
		sent++
	}
	return sent, nil
}

// queuedMessage is the queue message for a stored event. It carries the config
// id, not the signing secret: Deliver loads the secret from the config.
func queuedMessage(event models.WebhookEvent, attempt int) types.WebhookMessage {
	msg := types.WebhookMessage{
		EventID:     event.ID.String(),
		EventType:   types.EventType(event.EventType),
		Payload:     event.Payload,
		DeliveryURL: event.DeliveryURL,
		Attempt:     attempt,
	}
	if event.WebhookConfigID != nil {
		msg.ConfigID = event.WebhookConfigID.String()
	}
	if event.TransactionID != nil {
		msg.TransactionID = event.TransactionID.String()
	}
	return msg
}
