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

	"github.com/macrowallets/waas/pkg/types"
)

func webhookMessage() types.WebhookMessage {
	return types.WebhookMessage{
		EventID:       "evt-123",
		TransactionID: "tx-456",
		EventType:     types.EventDepositConfirmed,
		Payload:       `{"amount":"100"}`,
		DeliveryURL:   "https://example.com/wh",
		ConfigID:      "11111111-1111-1111-1111-111111111111",
		Attempt:       1,
	}
}

func TestSendWebhook_KeepsTheEncodedBodyAndAttribute(t *testing.T) {
	var got sqsRequest
	client := New(sqsClient(t, func(call sqsRequest, w http.ResponseWriter) {
		got = call
		writeSQSOk(w)
	}), "https://sqs.example/webhook")
	msg := webhookMessage()

	if err := client.SendWebhook(context.Background(), msg); err != nil {
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
	want, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := jsonString(t, got.body, "MessageBody")
	if body != string(want) {
		t.Fatal("encoded body changed")
	}
	if strings.Contains(body, `"secret"`) {
		t.Fatal("queued webhook body carries a secret")
	}
	attribute := messageAttribute(t, got.body, "event_type")
	if attribute["DataType"] != "String" || attribute["StringValue"] != string(msg.EventType) {
		t.Fatal("string attribute changed")
	}
	if len(messageAttributes(t, got.body)) != 1 {
		t.Fatalf("attribute count = %d", len(messageAttributes(t, got.body)))
	}
}

func TestSendWebhook_EmptyURLSkipsSQS(t *testing.T) {
	client := New(sqsClient(t, func(sqsRequest, http.ResponseWriter) {
		t.Fatal("an unconfigured queue must not reach SQS")
	}), "")

	if err := client.SendWebhook(context.Background(), webhookMessage()); err != nil {
		t.Fatalf("empty URL: %v", err)
	}
}

func TestSendWebhook_WrapsTheAPIError(t *testing.T) {
	client := New(sqsClient(t, func(_ sqsRequest, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		w.Header().Set("X-Amzn-ErrorType", "InvalidParameterValue")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"__type": "InvalidParameterValue", "message": "boom"})
	}), "https://sqs.example/webhook")

	err := client.SendWebhook(context.Background(), webhookMessage())
	if err == nil || !strings.HasPrefix(err.Error(), "sqs send: ") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v", err)
	}
}

func TestSendWebhook_ForwardsACanceledContext(t *testing.T) {
	client := New(sqsClient(t, func(sqsRequest, http.ResponseWriter) {
		t.Fatal("a canceled context must not reach SQS")
	}), "https://sqs.example/webhook")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.SendWebhook(ctx, webhookMessage())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
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
