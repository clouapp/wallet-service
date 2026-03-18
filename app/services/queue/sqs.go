package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/macromarkets/vault/pkg/types"
)

// ---------------------------------------------------------------------------
// SQSClient — thin wrapper over AWS SQS.
// Knows the queue URLs, handles JSON marshal, that's it.
// ---------------------------------------------------------------------------

// Sender defines the interface for sending messages to queues
type Sender interface {
	SendWebhook(ctx context.Context, msg types.WebhookMessage) error
	SendWithdrawal(ctx context.Context, msg types.WithdrawalMessage) error
}

type QueueURLs struct {
	Webhook    string
	Withdrawal string
}

type SQSClient struct {
	client *sqs.Client
	urls   QueueURLs
}

func NewSQSClient(client *sqs.Client, urls QueueURLs) *SQSClient {
	return &SQSClient{client: client, urls: urls}
}

// SendWebhook enqueues a webhook delivery job.
func (q *SQSClient) SendWebhook(ctx context.Context, msg types.WebhookMessage) error {
	return q.send(ctx, q.urls.Webhook, msg, map[string]string{
		"event_type": string(msg.EventType),
	})
}

// SendWithdrawal enqueues a withdrawal processing job.
// Uses transaction ID as dedup key (SQS FIFO not needed — idempotency at app level).
func (q *SQSClient) SendWithdrawal(ctx context.Context, msg types.WithdrawalMessage) error {
	return q.send(ctx, q.urls.Withdrawal, msg, map[string]string{
		"chain": msg.ChainID,
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

	msgAttrs := make(map[string]sqstypes.MessageAttributeValue, len(attrs))
	for k, v := range attrs {
		msgAttrs[k] = sqstypes.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(v),
		}
	}

	input := &sqs.SendMessageInput{
		QueueUrl:          aws.String(queueURL),
		MessageBody:       aws.String(string(body)),
		MessageAttributes: msgAttrs,
	}

	_, err = q.client.SendMessage(ctx, input)
	if err != nil {
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

	entries := make([]sqstypes.SendMessageBatchRequestEntry, 0, len(messages))
	for i, msg := range messages {
		body, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		entries = append(entries, sqstypes.SendMessageBatchRequestEntry{
			Id:          aws.String(fmt.Sprintf("msg-%d", i)),
			MessageBody: aws.String(string(body)),
		})
	}

	// SQS batch max is 10
	for i := 0; i < len(entries); i += 10 {
		end := i + 10
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[i:end]

		_, err := q.client.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: aws.String(queueURL),
			Entries:  batch,
		})
		if err != nil {
			return fmt.Errorf("sqs batch send: %w", err)
		}
	}

	return nil
}
