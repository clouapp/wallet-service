package sqs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// fakePollAPI hands out the queued batches in order, then cancels the run.
type fakePollAPI struct {
	mu       sync.Mutex
	batches  [][]sqstypes.Message
	errs     []error
	receives int
	deleted  []string
	cancel   context.CancelFunc
}

func (f *fakePollAPI) ReceiveMessage(_ context.Context, in *awssqs.ReceiveMessageInput, _ ...func(*awssqs.Options)) (*awssqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receives++
	if aws.ToString(in.QueueUrl) != "https://sqs.test/queue" || in.MaxNumberOfMessages != 5 || in.WaitTimeSeconds != 20 {
		return nil, errors.New("unexpected receive input")
	}
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return nil, err
	}
	if len(f.batches) == 0 {
		f.cancel()
		return &awssqs.ReceiveMessageOutput{}, nil
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	return &awssqs.ReceiveMessageOutput{Messages: batch}, nil
}

func (f *fakePollAPI) DeleteMessageBatch(_ context.Context, in *awssqs.DeleteMessageBatchInput, _ ...func(*awssqs.Options)) (*awssqs.DeleteMessageBatchOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, entry := range in.Entries {
		f.deleted = append(f.deleted, aws.ToString(entry.ReceiptHandle))
	}
	return &awssqs.DeleteMessageBatchOutput{}, nil
}

func message(id string) sqstypes.Message {
	return sqstypes.Message{
		MessageId:     aws.String(id),
		ReceiptHandle: aws.String("receipt-" + id),
		Body:          aws.String(`{"event_id":"` + id + `"}`),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"event_type": {DataType: aws.String("String"), StringValue: aws.String("deposit.confirmed")},
		},
	}
}

func newTestPoller(api *fakePollAPI) *Poller {
	return &Poller{api: api, queueURL: "https://sqs.test/queue", backoff: time.Millisecond}
}

func runPoller(t *testing.T, api *fakePollAPI, handle Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.cancel = cancel
	if err := newTestPoller(api).Run(ctx, handle); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestPoller_Run_DeletesWhatTheHandlerDidNotReportAsFailed(t *testing.T) {
	api := &fakePollAPI{batches: [][]sqstypes.Message{{message("a"), message("b")}}}

	runPoller(t, api, func(_ context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
		return events.SQSEventResponse{BatchItemFailures: []events.SQSBatchItemFailure{{ItemIdentifier: "a"}}}, nil
	})

	if len(api.deleted) != 1 || api.deleted[0] != "receipt-b" {
		t.Fatalf("deleted = %v, want only receipt-b", api.deleted)
	}
}

func TestPoller_Run_DeletesNothingWhenTheHandlerErrs(t *testing.T) {
	api := &fakePollAPI{batches: [][]sqstypes.Message{{message("a"), message("b")}}}

	runPoller(t, api, func(context.Context, events.SQSEvent) (events.SQSEventResponse, error) {
		return events.SQSEventResponse{}, errors.New("handler down")
	})

	if len(api.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", api.deleted)
	}
}

func TestPoller_Run_HandsTheHandlerTheSQSRecords(t *testing.T) {
	api := &fakePollAPI{batches: [][]sqstypes.Message{{message("a")}}}
	var got events.SQSEvent

	runPoller(t, api, func(_ context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
		got = event
		return events.SQSEventResponse{}, nil
	})

	if len(got.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(got.Records))
	}
	record := got.Records[0]
	if record.MessageId != "a" || record.ReceiptHandle != "receipt-a" || record.Body != `{"event_id":"a"}` {
		t.Fatalf("record = %+v", record)
	}
	if v := record.MessageAttributes["event_type"].StringValue; v == nil || *v != "deposit.confirmed" {
		t.Fatalf("event_type attribute = %v", v)
	}
}

func TestPoller_Run_BacksOffAfterAReceiveError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := &fakePollAPI{errs: []error{errors.New("throttled")}, cancel: cancel}

	if err := newTestPoller(api).Run(ctx, func(context.Context, events.SQSEvent) (events.SQSEventResponse, error) {
		return events.SQSEventResponse{}, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if api.receives != 2 {
		t.Fatalf("receives = %d, want a retry after the back-off", api.receives)
	}
}

func TestPoller_Run_StopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &fakePollAPI{batches: [][]sqstypes.Message{{message("a")}}}

	if err := newTestPoller(api).Run(ctx, func(context.Context, events.SQSEvent) (events.SQSEventResponse, error) {
		t.Fatal("handler ran after the context ended")
		return events.SQSEventResponse{}, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if api.receives != 0 {
		t.Fatalf("receives = %d, want none", api.receives)
	}
}

func TestPoller_Run_KeepsPollingAfterAnEmptyReceive(t *testing.T) {
	api := &fakePollAPI{batches: [][]sqstypes.Message{{}, {message("a")}}}
	handled := 0

	runPoller(t, api, func(context.Context, events.SQSEvent) (events.SQSEventResponse, error) {
		handled++
		return events.SQSEventResponse{}, nil
	})

	if handled != 1 || len(api.deleted) != 1 {
		t.Fatalf("handled = %d, deleted = %v; want the second receive delivered", handled, api.deleted)
	}
}

// A stop signal mid-batch must not cut the deliveries: one posted but not yet
// recorded would be posted again.
func TestPoller_Run_FinishesTheBatchInFlightWhenStopping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := &fakePollAPI{batches: [][]sqstypes.Message{{message("a")}}, cancel: cancel}

	if err := newTestPoller(api).Run(ctx, func(batchCtx context.Context, _ events.SQSEvent) (events.SQSEventResponse, error) {
		cancel()
		if batchCtx.Err() != nil {
			t.Fatal("the batch context ended with the stop signal")
		}
		if _, ok := batchCtx.Deadline(); !ok {
			t.Fatal("the batch has no deadline")
		}
		return events.SQSEventResponse{}, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(api.deleted) != 1 {
		t.Fatalf("deleted = %v, want the finished batch deleted", api.deleted)
	}
}
