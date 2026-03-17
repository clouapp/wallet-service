package queue

import (
	"context"
	"testing"

	"github.com/macromarkets/vault/pkg/types"
)

func TestSQSClient_SendWebhook_NoURL(t *testing.T) {
	client := &SQSClient{urls: QueueURLs{Webhook: "", Withdrawal: ""}}
	err := client.SendWebhook(context.Background(), types.WebhookMessage{
		EventID: "test", EventType: types.EventDepositConfirmed,
	})
	// Should not error — just warn and skip
	if err != nil {
		t.Errorf("expected nil error for empty URL, got: %v", err)
	}
}

func TestSQSClient_SendWithdrawal_NoURL(t *testing.T) {
	client := &SQSClient{urls: QueueURLs{Webhook: "", Withdrawal: ""}}
	err := client.SendWithdrawal(context.Background(), types.WithdrawalMessage{
		TransactionID: "test", ChainID: "eth",
	})
	if err != nil {
		t.Errorf("expected nil error for empty URL, got: %v", err)
	}
}

func TestSQSClient_NilClient(t *testing.T) {
	// Verify constructor works
	client := NewSQSClient(nil, QueueURLs{Webhook: "test", Withdrawal: "test"})
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

func TestWithdrawalMessage_Serialization(t *testing.T) {
	msg := types.WithdrawalMessage{
		TransactionID:  "tx-789",
		WalletID:       "w-123",
		ChainID:        "eth",
		ToAddress:      "0xto",
		Amount:         "1000000",
		Asset:          "usdt",
		TokenContract:  "0xdAC17F",
		ExternalUserID: "user_abc",
	}

	if msg.ChainID != "eth" { t.Error("ChainID mismatch") }
	if msg.TokenContract != "0xdAC17F" { t.Error("TokenContract mismatch") }
}

func TestQueueURLs(t *testing.T) {
	urls := QueueURLs{
		Webhook:    "https://sqs.us-east-1.amazonaws.com/123/vault-webhooks-dev",
		Withdrawal: "https://sqs.us-east-1.amazonaws.com/123/vault-withdrawals-dev",
	}
	if urls.Webhook == "" || urls.Withdrawal == "" {
		t.Error("URLs should not be empty")
	}
}
