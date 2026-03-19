package types

import (
	"testing"
)

func TestEventTypes(t *testing.T) {
	events := []EventType{
		EventDepositPending, EventDepositConfirming, EventDepositConfirmed, EventDepositFailed,
		EventWithdrawalPending, EventWithdrawalSigned, EventWithdrawalBroadcast, EventWithdrawalConfirmed, EventWithdrawalFailed,
	}

	seen := make(map[EventType]bool)
	for _, e := range events {
		if seen[e] {
			t.Errorf("duplicate event type: %s", e)
		}
		seen[e] = true
		if string(e) == "" {
			t.Error("event type should not be empty")
		}
	}

	if len(events) != 9 {
		t.Errorf("expected 9 event types, got %d", len(events))
	}
}

func TestTxStatuses(t *testing.T) {
	statuses := []TxStatus{TxStatusPending, TxStatusConfirming, TxStatusConfirmed, TxStatusFailed}
	for _, s := range statuses {
		if string(s) == "" {
			t.Error("status should not be empty")
		}
	}
	if len(statuses) != 4 {
		t.Errorf("expected 4 statuses, got %d", len(statuses))
	}
}

func TestDepositScanEvent(t *testing.T) {
	evt := DepositScanEvent{Chain: "eth"}
	if evt.Chain != "eth" {
		t.Errorf("expected eth, got %s", evt.Chain)
	}
}

func TestWebhookMessage_Fields(t *testing.T) {
	msg := WebhookMessage{
		EventID: "e1", TransactionID: "t1", EventType: EventDepositConfirmed,
		Payload: "{}", DeliveryURL: "https://example.com", Secret: "s", Attempt: 3,
	}
	if msg.EventID != "e1" { t.Error("EventID") }
	if msg.TransactionID != "t1" { t.Error("TransactionID") }
	if msg.EventType != EventDepositConfirmed { t.Error("EventType") }
	if msg.Attempt != 3 { t.Error("Attempt") }
}

func TestToken_Fields(t *testing.T) {
	tok := Token{Symbol: "usdt", Name: "Tether USD", Contract: "0xdAC17F", Decimals: 6, ChainID: "eth"}
	if tok.Symbol != "usdt" { t.Error("Symbol") }
	if tok.Decimals != 6 { t.Error("Decimals") }
}
