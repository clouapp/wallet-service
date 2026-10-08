package sqs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/macrowallets/waas/app/services/queue"
)

// api is the subset of the AWS SQS client this adapter calls.
type api interface {
	SendMessage(ctx context.Context, params *awssqs.SendMessageInput, optFns ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error)
	SendMessageBatch(ctx context.Context, params *awssqs.SendMessageBatchInput, optFns ...func(*awssqs.Options)) (*awssqs.SendMessageBatchOutput, error)
}

// Client calls SendMessage and SendMessageBatch. The service keeps encoding and the batch size.
type Client struct {
	api api
}

var _ queue.Transport = (*Client)(nil)
var _ api = (*awssqs.Client)(nil)

// New wraps client. A nil client returns a nil transport so the service keeps its nil-client path.
func New(client *awssqs.Client) queue.Transport {
	if client == nil {
		return nil
	}
	return &Client{api: client}
}

// Send publishes one body. Attributes stay string attributes. The body is not logged.
func (c *Client) Send(ctx context.Context, queueURL, body string, attributes map[string]string) error {
	if c == nil || c.api == nil {
		return fmt.Errorf("sqs transport: client is nil")
	}

	_, err := c.api.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:          aws.String(queueURL),
		MessageBody:       aws.String(body),
		MessageAttributes: stringAttributes(attributes),
	})
	return err
}

// SendBatch publishes one batch. The caller already split entries into groups of ten.
func (c *Client) SendBatch(ctx context.Context, queueURL string, entries []queue.BatchEntry) error {
	if c == nil || c.api == nil {
		return fmt.Errorf("sqs transport: client is nil")
	}

	batch := make([]sqstypes.SendMessageBatchRequestEntry, 0, len(entries))
	for _, entry := range entries {
		batch = append(batch, sqstypes.SendMessageBatchRequestEntry{
			Id:          aws.String(entry.ID),
			MessageBody: aws.String(entry.Body),
		})
	}

	_, err := c.api.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{
		QueueUrl: aws.String(queueURL),
		Entries:  batch,
	})
	return err
}

func stringAttributes(attributes map[string]string) map[string]sqstypes.MessageAttributeValue {
	encoded := make(map[string]sqstypes.MessageAttributeValue, len(attributes))
	for key, value := range attributes {
		encoded[key] = sqstypes.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(value),
		}
	}
	return encoded
}
