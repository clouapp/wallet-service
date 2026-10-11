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
	if aws.ToString(in.QueueUrl) != "https://sqs.test/queue" || in.MaxNumberOfMessages != 10 || in.WaitTimeSeconds != 20 {
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

func newTestPoller(api *fakePollAPI) (*Poller, *[]time.Duration) {
	slept := &[]time.Duration{}
	return &Poller{
		api:      api,
		queueURL: "https://sqs.test/queue",
		sleep:    func(_ context.Context, d time.Duration) { *slept = append(*slept, d) },
	}, slept
}

func runPoller(t *testing.T, api *fakePollAPI, handle Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.cancel = cancel
	poller, _ := newTestPoller(api)
	if err := poller.Run(ctx, handle); err != nil {
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
	poller, slept := newTestPoller(api)

	if err := poller.Run(ctx, func(context.Context, events.SQSEvent) (events.SQSEventResponse, error) {
		return events.SQSEventResponse{}, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(*slept) != 1 || (*slept)[0] != receiveBackoff {
		t.Fatalf("slept = %v, want one %v back-off", *slept, receiveBackoff)
	}
	if api.receives != 2 {
		t.Fatalf("receives = %d, want a retry after the back-off", api.receives)
	}
}

func TestPoller_Run_StopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &fakePollAPI{batches: [][]sqstypes.Message{{message("a")}}}
	poller, _ := newTestPoller(api)

	if err := poller.Run(ctx, func(context.Context, events.SQSEvent) (events.SQSEventResponse, error) {
		t.Fatal("handler ran after the context ended")
		return events.SQSEventResponse{}, nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if api.receives != 0 {
		t.Fatalf("receives = %d, want none", api.receives)
	}
}
