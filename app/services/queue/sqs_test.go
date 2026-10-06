package queue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/macrowallets/waas/pkg/types"
)

func TestSQSClient_SendWebhook_NoURL(t *testing.T) {
	client := &SQSClient{urls: QueueURLs{Webhook: ""}}
	err := client.SendWebhook(context.Background(), types.WebhookMessage{
		EventID: "test", EventType: types.EventDepositConfirmed,
	})
	// Should not error — just warn and skip
	if err != nil {
		t.Errorf("expected nil error for empty URL, got: %v", err)
	}
}

func TestNew_SQS_ClientKeepsItsDependencies(t *testing.T) {
	transport := &stubTransport{}
	urls := QueueURLs{Webhook: "https://sqs.example/webhook"}
	client := NewSQSClient(SQSClientDeps{Transport: transport, URLs: urls})
	if client == nil || client.transport != transport || client.urls != urls {
		t.Fatal("sqs client did not keep its dependencies")
	}

	bare := NewSQSClient(SQSClientDeps{})
	if bare == nil || bare.transport != nil || bare.urls != (QueueURLs{}) {
		t.Fatal("missing dependencies were not left unset")
	}
}

func TestWebhook_Message_Serialization(t *testing.T) {
	msg := types.WebhookMessage{
		EventID:       "evt-123",
		TransactionID: "tx-456",
		EventType:     types.EventDepositConfirmed,
		Payload:       `{"amount":"100"}`,
		DeliveryURL:   "https://example.com/wh",
		ConfigID:      "11111111-1111-1111-1111-111111111111",
		Attempt:       1,
	}

	if msg.EventID != "evt-123" {
		t.Error("EventID mismatch")
	}
	if msg.EventType != types.EventDepositConfirmed {
		t.Error("EventType mismatch")
	}
	if msg.Attempt != 1 {
		t.Error("Attempt mismatch")
	}
}

func TestQueue_UR_Ls(t *testing.T) {
	urls := QueueURLs{
		Webhook: "https://sqs.us-east-1.amazonaws.com/123/vault-webhooks-dev",
	}
	if urls.Webhook == "" {
		t.Error("Webhook URL should not be empty")
	}
}

func TestSend_Webhook_KeepsTheEncodedBodyAndAttribute(t *testing.T) {
	msg := types.WebhookMessage{
		EventID:       "evt-123",
		TransactionID: "tx-456",
		EventType:     types.EventDepositConfirmed,
		Payload:       `{"amount":"100"}`,
		DeliveryURL:   "https://example.com/wh",
		ConfigID:      "11111111-1111-1111-1111-111111111111",
		Attempt:       1,
	}
	transport := &stubTransport{}
	client := NewSQSClient(SQSClientDeps{Transport: transport, URLs: QueueURLs{Webhook: "https://sqs.example/webhook"}})

	if err := client.SendWebhook(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}

	want, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if transport.body != string(want) {
		t.Fatal("encoded body changed")
	}
	if transport.queueURL != "https://sqs.example/webhook" {
		t.Fatalf("queue URL = %q", transport.queueURL)
	}
	if len(transport.attributes) != 1 || transport.attributes["event_type"] != string(msg.EventType) {
		t.Fatalf("event_type attribute = %v", transport.attributes["event_type"])
	}
	if strings.Contains(transport.body, `"secret"`) {
		t.Fatal("queued webhook body carries a secret")
	}
}

func TestSend_Webhook_EmptyURLSkipsTheTransport(t *testing.T) {
	transport := &stubTransport{err: errors.New("should not be called")}
	client := NewSQSClient(SQSClientDeps{Transport: transport})

	err := client.SendWebhook(context.Background(), types.WebhookMessage{EventType: types.EventDepositConfirmed})
	if err != nil {
		t.Fatalf("empty URL: %v", err)
	}
	if transport.calls != 0 {
		t.Fatalf("transport calls = %d", transport.calls)
	}
}

func TestSend_Webhook_WrapsTheTransportError(t *testing.T) {
	transport := &stubTransport{err: errors.New("boom")}
	client := NewSQSClient(SQSClientDeps{Transport: transport, URLs: QueueURLs{Webhook: "https://sqs.example/webhook"}})

	err := client.SendWebhook(context.Background(), types.WebhookMessage{EventType: types.EventDepositConfirmed})
	if err == nil || err.Error() != "sqs send: boom" {
		t.Fatalf("error = %v", err)
	}
}

func TestSend_Batch_KeepsEntryIDsAndChunkSize(t *testing.T) {
	messages := make([]interface{}, 11)
	for i := range messages {
		messages[i] = batchItem{N: i}
	}
	transport := &stubTransport{}
	client := NewSQSClient(SQSClientDeps{Transport: transport})

	if err := client.SendBatch(context.Background(), "https://sqs.example/batch", messages); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(transport.batches) != 2 {
		t.Fatalf("batches = %d", len(transport.batches))
	}
	if len(transport.batches[0]) != maxBatchEntries || len(transport.batches[1]) != 1 {
		t.Fatalf("chunk sizes = %d, %d", len(transport.batches[0]), len(transport.batches[1]))
	}
	if transport.batches[0][0].ID != "msg-0" || transport.batches[1][0].ID != "msg-10" {
		t.Fatal("batch entry ids changed")
	}
	if transport.batches[0][0].Body != `{"n":0}` || transport.batches[1][0].Body != `{"n":10}` {
		t.Fatal("batch bodies changed")
	}
}

func TestSend_Batch_SkipsAnEntryThatCannotBeEncoded(t *testing.T) {
	transport := &stubTransport{}
	client := NewSQSClient(SQSClientDeps{Transport: transport})
	messages := []interface{}{make(chan int), batchItem{N: 2}}

	if err := client.SendBatch(context.Background(), "https://sqs.example/batch", messages); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(transport.batches) != 1 || len(transport.batches[0]) != 1 {
		t.Fatalf("batches = %#v", len(transport.batches))
	}
	if transport.batches[0][0].ID != "msg-1" || transport.batches[0][0].Body != `{"n":2}` {
		t.Fatal("skipped entry changed the following id or body")
	}
}

func TestSend_Batch_EmptyMessagesSkipTheTransport(t *testing.T) {
	transport := &stubTransport{err: errors.New("should not be called")}
	client := NewSQSClient(SQSClientDeps{Transport: transport})

	if err := client.SendBatch(context.Background(), "https://sqs.example/batch", nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if transport.calls != 0 {
		t.Fatalf("transport calls = %d", transport.calls)
	}
}

func TestSend_Batch_WrapsTheTransportError(t *testing.T) {
	transport := &stubTransport{err: errors.New("boom")}
	client := NewSQSClient(SQSClientDeps{Transport: transport})

	err := client.SendBatch(context.Background(), "https://sqs.example/batch", []interface{}{batchItem{N: 1}})
	if err == nil || err.Error() != "sqs batch send: boom" {
		t.Fatalf("error = %v", err)
	}
}

type batchItem struct {
	N int `json:"n"`
}

type stubTransport struct {
	err        error
	calls      int
	queueURL   string
	body       string
	attributes map[string]string
	batches    [][]BatchEntry
}

func (s *stubTransport) Send(_ context.Context, queueURL, body string, attributes map[string]string) error {
	s.calls++
	s.queueURL = queueURL
	s.body = body
	s.attributes = attributes
	return s.err
}

func (s *stubTransport) SendBatch(_ context.Context, queueURL string, entries []BatchEntry) error {
	s.calls++
	s.queueURL = queueURL
	copied := append([]BatchEntry(nil), entries...)
	s.batches = append(s.batches, copied)
	return s.err
}
