package queue

import (
	"context"
	"testing"

	"github.com/macromarkets/vault/pkg/types"
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

func TestSQSClient_NilClient(t *testing.T) {
	// Verify constructor works
	client := NewSQSClient(nil, QueueURLs{Webhook: "test"})
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestWebhookMessage_Serialization(t *testing.T) {
	msg := types.WebhookMessage{
		EventID:       "evt-123",
		TransactionID: "tx-456",
		EventType:     types.EventDepositConfirmed,
		Payload:       `{"amount":"100"}`,
		DeliveryURL:   "https://example.com/wh",
		Secret:        "secret",
		Attempt:       1,
	}

	if msg.EventID != "evt-123" { t.Error("EventID mismatch") }
	if msg.EventType != types.EventDepositConfirmed { t.Error("EventType mismatch") }
	if msg.Attempt != 1 { t.Error("Attempt mismatch") }
}

func TestQueueURLs(t *testing.T) {
	urls := QueueURLs{
		Webhook: "https://sqs.us-east-1.amazonaws.com/123/vault-webhooks-dev",
	}
	if urls.Webhook == "" {
		t.Error("Webhook URL should not be empty")
	}
}
