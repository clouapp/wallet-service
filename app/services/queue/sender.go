package queue

import (
	"context"

	"github.com/macrowallets/waas/pkg/types"
)

// Sender defines the interface for sending messages to queues.
type Sender interface {
	SendWebhook(ctx context.Context, msg types.WebhookMessage) error
}
