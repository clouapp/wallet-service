package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/pkg/types"
)

// api is the subset of the AWS SQS client this adapter calls.
type api interface {
	SendMessage(ctx context.Context, params *awssqs.SendMessageInput, optFns ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error)
}

// Client encodes a webhook delivery job as JSON and publishes it to the webhook
// queue. The consumer is a Lambda on an event source mapping, so nothing here reads.
type Client struct {
	api        api
	webhookURL string
}

var _ queue.Sender = (*Client)(nil)
var _ api = (*awssqs.Client)(nil)

// New publishes webhook jobs to webhookURL with client. An empty webhookURL makes
// SendWebhook log and skip, so a process without the queue still runs.
func New(client *awssqs.Client, webhookURL string) *Client {
	return &Client{api: client, webhookURL: webhookURL}
}

// SendWebhook enqueues a webhook delivery job. The body is not logged.
func (c *Client) SendWebhook(ctx context.Context, msg types.WebhookMessage) error {
	if c.webhookURL == "" {
		slog.Warn("queue URL not configured, skipping send")
		return nil
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	_, err = c.api.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    aws.String(c.webhookURL),
		MessageBody: aws.String(string(body)),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"event_type": {DataType: aws.String("String"), StringValue: aws.String(string(msg.EventType))},
		},
	})
	if err != nil {
		return fmt.Errorf("sqs send: %w", err)
	}

	slog.Debug("message sent to SQS", "queue", c.webhookURL)
	return nil
}
