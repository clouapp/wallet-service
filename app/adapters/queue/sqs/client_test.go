package sqs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/macrowallets/waas/app/services/queue"
)

func TestNew_Returns_NilForANilClient(t *testing.T) {
	if New(nil) != nil {
		t.Fatal("expected a nil transport when SQS is not configured")
	}
}

func TestSend_Copies_TheBodyAndStringAttributes(t *testing.T) {
	var got sqsRequest
	client := New(sqsClient(t, func(call sqsRequest, w http.ResponseWriter) {
		got = call
		writeSQSOk(w)
	}))
	attributes := map[string]string{"event_type": "deposit.confirmed"}

	if err := client.Send(context.Background(), "https://sqs.example/webhook", `{"n":1}`, attributes); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.target != "AmazonSQS.SendMessage" || got.method != http.MethodPost || got.path != "/" {
		t.Fatalf("request %s %s target %s", got.method, got.path, got.target)
	}
	if !strings.HasPrefix(got.auth, "AWS4-HMAC-SHA256") || !strings.Contains(got.contentType, "application/x-amz-json-1.0") {
		t.Fatalf("auth %q content-type %q", got.auth, got.contentType)
	}
	if jsonString(t, got.body, "QueueUrl") != "https://sqs.example/webhook" {
		t.Fatal("queue URL changed")
	}
	if jsonString(t, got.body, "MessageBody") != `{"n":1}` {
		t.Fatal("message body changed")
	}
	attribute := messageAttribute(t, got.body, "event_type")
	if attribute["DataType"] != "String" || attribute["StringValue"] != "deposit.confirmed" {
		t.Fatal("string attribute changed")
	}
	if len(messageAttributes(t, got.body)) != 1 {
		t.Fatalf("attribute count = %d", len(messageAttributes(t, got.body)))
	}
}

func TestSend_Batch_CopiesEntriesInOrder(t *testing.T) {
	var got sqsRequest
	client := New(sqsClient(t, func(call sqsRequest, w http.ResponseWriter) {
		got = call
		writeSQSOk(w)
	}))
	entries := []queue.BatchEntry{
		{ID: "msg-0", Body: `{"n":0}`},
		{ID: "msg-1", Body: `{"n":1}`},
	}

	if err := client.SendBatch(context.Background(), "https://sqs.example/batch", entries); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if got.target != "AmazonSQS.SendMessageBatch" || jsonString(t, got.body, "QueueUrl") != "https://sqs.example/batch" {
		t.Fatal("batch queue URL changed")
	}
	batch := batchEntries(t, got.body)
	if len(batch) != 2 {
		t.Fatal("expected both batch entries")
	}
	if batch[0]["Id"] != "msg-0" || batch[0]["MessageBody"] != `{"n":0}` {
		t.Fatal("first batch entry changed")
	}
	if batch[1]["Id"] != "msg-1" || batch[1]["MessageBody"] != `{"n":1}` {
		t.Fatal("second batch entry changed")
	}
	if _, ok := batch[0]["MessageAttributes"]; ok {
		t.Fatal("batch entries gained attributes")
	}
	if _, ok := batch[1]["MessageAttributes"]; ok {
		t.Fatal("batch entries gained attributes")
	}
}

func TestSend_Forwards_TheAPIError(t *testing.T) {
	client := New(sqsClient(t, func(_ sqsRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.Header().Set("X-Amzn-ErrorType", "InvalidParameterValue")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"__type": "InvalidParameterValue", "message": "boom"})
	}))
	err := client.Send(context.Background(), "https://sqs.example/webhook", `{"n":1}`, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v", err)
	}
}

func TestSend_Forwards_ACanceledContext(t *testing.T) {
	client := New(sqsClient(t, func(sqsRequest, http.ResponseWriter) {
		t.Fatal("a canceled context must not reach SQS")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Send(ctx, "https://sqs.example/webhook", `{"n":1}`, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestNil_Client_ReportsAMissingClient(t *testing.T) {
	var client *Client
	if err := client.Send(context.Background(), "https://sqs.example/webhook", `{"n":1}`, nil); err == nil {
		t.Fatal("expected error for a nil client")
	}
	if err := client.SendBatch(context.Background(), "https://sqs.example/batch", nil); err == nil {
		t.Fatal("expected error for a nil client batch")
	}
}

type sqsRequest struct {
	method      string
	path        string
	target      string
	auth        string
	contentType string
	body        map[string]json.RawMessage
}

func sqsClient(t *testing.T, respond func(sqsRequest, http.ResponseWriter)) *awssqs.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		body := map[string]json.RawMessage{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("body: %v", err)
			}
		}
		respond(sqsRequest{
			method:      r.Method,
			path:        r.URL.Path,
			target:      r.Header.Get("X-Amz-Target"),
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		}, w)
	}))
	t.Cleanup(server.Close)
	return awssqs.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}, func(o *awssqs.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.Retryer = retry.AddWithMaxAttempts(retry.NewStandard(), 1)
	})
}

func writeSQSOk(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	_, _ = w.Write([]byte("{}"))
}

func jsonString(t *testing.T, body map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := body[key]
	if !ok {
		t.Fatalf("missing %s", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func messageAttributes(t *testing.T, body map[string]json.RawMessage) map[string]map[string]string {
	t.Helper()
	raw, ok := body["MessageAttributes"]
	if !ok {
		t.Fatal("missing MessageAttributes")
	}
	var attributes map[string]map[string]string
	if err := json.Unmarshal(raw, &attributes); err != nil {
		t.Fatal(err)
	}
	return attributes
}

func messageAttribute(t *testing.T, body map[string]json.RawMessage, name string) map[string]string {
	t.Helper()
	attribute, ok := messageAttributes(t, body)[name]
	if !ok {
		t.Fatalf("missing attribute %s", name)
	}
	return attribute
}

func batchEntries(t *testing.T, body map[string]json.RawMessage) []map[string]any {
	t.Helper()
	raw, ok := body["Entries"]
	if !ok {
		t.Fatal("missing Entries")
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}
