package sqs

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/macrowallets/waas/app/services/queue"
)

func TestNewReturnsNilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil transport when SQS is not configured")
	}
}

func TestSendCopiesTheBodyAndStringAttributes(t *testing.T) {
	fake := &fakeAPI{}
	client := &Client{api: fake}
	attributes := map[string]string{"event_type": "deposit.confirmed"}

	if err := client.Send(context.Background(), "https://sqs.example/webhook", `{"n":1}`, attributes); err != nil {
		t.Fatalf("send: %v", err)
	}
	if fake.sendInput == nil {
		t.Fatal("expected SendMessage to be called")
	}
	if aws.ToString(fake.sendInput.QueueUrl) != "https://sqs.example/webhook" {
		t.Fatal("queue URL changed")
	}
	if aws.ToString(fake.sendInput.MessageBody) != `{"n":1}` {
		t.Fatal("message body changed")
	}
	attribute, ok := fake.sendInput.MessageAttributes["event_type"]
	if !ok || aws.ToString(attribute.DataType) != "String" || aws.ToString(attribute.StringValue) != "deposit.confirmed" {
		t.Fatal("string attribute changed")
	}
	if len(fake.sendInput.MessageAttributes) != 1 {
		t.Fatalf("attribute count = %d", len(fake.sendInput.MessageAttributes))
	}
}

func TestSendBatchCopiesEntriesInOrder(t *testing.T) {
	fake := &fakeAPI{}
	client := &Client{api: fake}
	entries := []queue.BatchEntry{
		{ID: "msg-0", Body: `{"n":0}`},
		{ID: "msg-1", Body: `{"n":1}`},
	}

	if err := client.SendBatch(context.Background(), "https://sqs.example/batch", entries); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if fake.batchInput == nil || len(fake.batchInput.Entries) != 2 {
		t.Fatal("expected both batch entries")
	}
	if aws.ToString(fake.batchInput.QueueUrl) != "https://sqs.example/batch" {
		t.Fatal("batch queue URL changed")
	}
	if aws.ToString(fake.batchInput.Entries[0].Id) != "msg-0" || aws.ToString(fake.batchInput.Entries[0].MessageBody) != `{"n":0}` {
		t.Fatal("first batch entry changed")
	}
	if aws.ToString(fake.batchInput.Entries[1].Id) != "msg-1" || aws.ToString(fake.batchInput.Entries[1].MessageBody) != `{"n":1}` {
		t.Fatal("second batch entry changed")
	}
	if fake.batchInput.Entries[0].MessageAttributes != nil || fake.batchInput.Entries[1].MessageAttributes != nil {
		t.Fatal("batch entries gained attributes")
	}
}

func TestSendForwardsTheAPIError(t *testing.T) {
	fake := &fakeAPI{err: errors.New("boom")}
	err := (&Client{api: fake}).Send(context.Background(), "https://sqs.example/webhook", `{"n":1}`, nil)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("error = %v", err)
	}
}

func TestSendForwardsACanceledContext(t *testing.T) {
	fake := &fakeAPI{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := (&Client{api: fake}).Send(ctx, "https://sqs.example/webhook", `{"n":1}`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestNilClientReportsAMissingClient(t *testing.T) {
	var client *Client
	if err := client.Send(context.Background(), "https://sqs.example/webhook", `{"n":1}`, nil); err == nil {
		t.Fatal("expected error for a nil client")
	}
	if err := client.SendBatch(context.Background(), "https://sqs.example/batch", nil); err == nil {
		t.Fatal("expected error for a nil client batch")
	}
}

type fakeAPI struct {
	sendInput  *awssqs.SendMessageInput
	batchInput *awssqs.SendMessageBatchInput
	err        error
}

func (f *fakeAPI) SendMessage(ctx context.Context, params *awssqs.SendMessageInput, _ ...func(*awssqs.Options)) (*awssqs.SendMessageOutput, error) {
	f.sendInput = params
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &awssqs.SendMessageOutput{}, f.err
}

func (f *fakeAPI) SendMessageBatch(ctx context.Context, params *awssqs.SendMessageBatchInput, _ ...func(*awssqs.Options)) (*awssqs.SendMessageBatchOutput, error) {
	f.batchInput = params
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &awssqs.SendMessageBatchOutput{}, f.err
}
