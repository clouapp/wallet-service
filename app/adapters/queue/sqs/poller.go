package sqs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	// pollBatchSize keeps a batch inside the queue's 60 s visibility timeout: five
	// deliveries at the 10 s delivery timeout each, as template.yaml's BatchSize.
	pollBatchSize   = 5
	pollWaitSeconds = 20
	// batchDeadline bounds a batch, below the visibility timeout. A batch runs to
	// its end even when the process is stopping: a delivery cut mid-flight may
	// have been posted without being recorded, and would be posted again.
	batchDeadline = 55 * time.Second
	// receiveBackoff spaces the receives after a failed one, so a missing
	// permission or an unreachable endpoint does not spin.
	receiveBackoff = 5 * time.Second
)

// Handler is the deployed SQS handler's shape (main.handleWebhookWorker).
type Handler func(context.Context, events.SQSEvent) (events.SQSEventResponse, error)

// pollAPI is the subset of the AWS SQS client the poller calls.
type pollAPI interface {
	ReceiveMessage(ctx context.Context, params *awssqs.ReceiveMessageInput, optFns ...func(*awssqs.Options)) (*awssqs.ReceiveMessageOutput, error)
	DeleteMessageBatch(ctx context.Context, params *awssqs.DeleteMessageBatchInput, optFns ...func(*awssqs.Options)) (*awssqs.DeleteMessageBatchOutput, error)
}

var _ pollAPI = (*awssqs.Client)(nil)

// Poller long-polls one queue and hands each batch to the deployed handler, as the
// Lambda event source mapping does: a message the handler does not report as failed
// is deleted; a failed one comes back after its visibility timeout and reaches the
// dead-letter queue after the queue's maxReceiveCount. It stands in for the Lambda
// on a host without one and must never run beside it on the same queue.
type Poller struct {
	api      pollAPI
	queueURL string
	backoff  time.Duration
}

// NewPoller polls queueURL with client.
func NewPoller(client *awssqs.Client, queueURL string) *Poller {
	return &Poller{api: client, queueURL: queueURL, backoff: receiveBackoff}
}

// Run receives until ctx ends, then returns nil. Receive and delete errors are
// logged, never fatal: an undeleted message is simply delivered again, and the
// handler skips an event it already delivered.
func (p *Poller) Run(ctx context.Context, handle Handler) error {
	for ctx.Err() == nil {
		out, err := p.api.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl:              aws.String(p.queueURL),
			MaxNumberOfMessages:   pollBatchSize,
			WaitTimeSeconds:       pollWaitSeconds,
			MessageAttributeNames: []string{"All"},
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			slog.Error("sqs receive failed", "queue", p.queueURL, "error", err)
			sleepContext(ctx, p.backoff)
			continue
		}
		if len(out.Messages) == 0 {
			continue
		}
		batchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), batchDeadline)
		p.handleBatch(batchCtx, out.Messages, handle)
		cancel()
	}
	return nil
}

func (p *Poller) handleBatch(ctx context.Context, messages []sqstypes.Message, handle Handler) {
	event := events.SQSEvent{Records: make([]events.SQSMessage, 0, len(messages))}
	for _, message := range messages {
		event.Records = append(event.Records, sqsRecord(message))
	}

	response, err := handle(ctx, event)
	if err != nil {
		slog.Error("sqs batch handler failed; the batch will be delivered again", "queue", p.queueURL, "error", err)
		return
	}
	failed := make(map[string]bool, len(response.BatchItemFailures))
	for _, failure := range response.BatchItemFailures {
		failed[failure.ItemIdentifier] = true
	}

	entries := make([]sqstypes.DeleteMessageBatchRequestEntry, 0, len(messages))
	for i, message := range messages {
		if failed[aws.ToString(message.MessageId)] {
			continue
		}
		entries = append(entries, sqstypes.DeleteMessageBatchRequestEntry{
			Id:            aws.String(fmt.Sprintf("m%d", i)),
			ReceiptHandle: message.ReceiptHandle,
		})
	}
	if len(entries) == 0 {
		return
	}
	out, err := p.api.DeleteMessageBatch(ctx, &awssqs.DeleteMessageBatchInput{
		QueueUrl: aws.String(p.queueURL),
		Entries:  entries,
	})
	if err != nil {
		slog.Error("sqs delete failed; the messages will be delivered again", "queue", p.queueURL, "error", err)
		return
	}
	for _, entry := range out.Failed {
		slog.Error("sqs delete refused; the message will be delivered again", "queue", p.queueURL, "code", aws.ToString(entry.Code))
	}
}

// sqsRecord is the Lambda SQS record of a received message. The body is passed
// through untouched and never logged.
func sqsRecord(message sqstypes.Message) events.SQSMessage {
	attributes := make(map[string]events.SQSMessageAttribute, len(message.MessageAttributes))
	for name, value := range message.MessageAttributes {
		attributes[name] = events.SQSMessageAttribute{
			DataType:    aws.ToString(value.DataType),
			StringValue: value.StringValue,
			BinaryValue: value.BinaryValue,
		}
	}
	return events.SQSMessage{
		MessageId:         aws.ToString(message.MessageId),
		ReceiptHandle:     aws.ToString(message.ReceiptHandle),
		Body:              aws.ToString(message.Body),
		Md5OfBody:         aws.ToString(message.MD5OfBody),
		Attributes:        message.Attributes,
		MessageAttributes: attributes,
		EventSource:       "aws:sqs",
	}
}

func sleepContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
