package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/macrowallets/waas/pkg/types"
)

// maxBatchEntries is the SQS SendMessageBatch limit.
const maxBatchEntries = 10

// Sender defines the interface for sending messages to queues.
type Sender interface {
	SendWebhook(ctx context.Context, msg types.WebhookMessage) error
}

// Transport sends already-encoded bodies. The provider supplies it.
// A nil Transport means the AWS client was not configured.
type Transport interface {
	Send(ctx context.Context, queueURL, body string, attributes map[string]string) error
	SendBatch(ctx context.Context, queueURL string, entries []BatchEntry) error
}

// BatchEntry is one already-encoded message. ID is the SQS batch entry id.
type BatchEntry struct {
	ID   string
	Body string
}

type QueueURLs struct {
	Webhook string
}

type SQSClient struct {
	transport Transport
	urls      QueueURLs
}

// SQSClientDeps is everything the SQS client needs. A nil Transport means the
// AWS client was not configured.
type SQSClientDeps struct {
	Transport Transport
	URLs      QueueURLs
}

// NewSQSClient wires the SQS client from SQSClientDeps.
func NewSQSClient(deps SQSClientDeps) *SQSClient {
	return &SQSClient{transport: deps.Transport, urls: deps.URLs}
}

// SendWebhook enqueues a webhook delivery job.
func (q *SQSClient) SendWebhook(ctx context.Context, msg types.WebhookMessage) error {
	return q.send(ctx, q.urls.Webhook, msg, map[string]string{
		"event_type": string(msg.EventType),
	})
}

func (q *SQSClient) send(ctx context.Context, queueURL string, payload interface{}, attrs map[string]string) error {
	if queueURL == "" {
		slog.Warn("queue URL not configured, skipping send")
		return nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	if err := q.transport.Send(ctx, queueURL, string(body), attrs); err != nil {
		return fmt.Errorf("sqs send: %w", err)
	}

	slog.Debug("message sent to SQS", "queue", queueURL)
	return nil
}

// SendBatch sends multiple messages to a queue in one API call (max 10).
func (q *SQSClient) SendBatch(ctx context.Context, queueURL string, messages []interface{}) error {
	if len(messages) == 0 {
		return nil
	}

	entries := make([]BatchEntry, 0, len(messages))
	for i, msg := range messages {
		body, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		entries = append(entries, BatchEntry{
			ID:   fmt.Sprintf("msg-%d", i),
			Body: string(body),
		})
	}

	for start := 0; start < len(entries); start += maxBatchEntries {
		end := start + maxBatchEntries
		if end > len(entries) {
			end = len(entries)
		}
		if err := q.transport.SendBatch(ctx, queueURL, entries[start:end]); err != nil {
			return fmt.Errorf("sqs batch send: %w", err)
		}
	}

	return nil
}
