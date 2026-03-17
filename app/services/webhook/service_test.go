package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/macromarkets/vault/pkg/types"
	"github.com/macromarkets/vault/tests/mocks"
)

func TestCreateConfig(t *testing.T) {
	db := mocks.TestDB(t)
	svc := NewService(db, nil)
	ctx := context.Background()

	cfg, err := svc.CreateConfig(ctx, "https://example.com/webhook", "secret123", []string{"deposit.confirmed", "withdrawal.confirmed"})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	if cfg.URL != "https://example.com/webhook" {
		t.Errorf("expected URL, got %s", cfg.URL)
	}
	if !cfg.IsActive {
		t.Error("expected active")
	}
}

func TestListConfigs(t *testing.T) {
	db := mocks.TestDB(t)
	svc := NewService(db, nil)
	ctx := context.Background()

	svc.CreateConfig(ctx, "https://a.com/wh", "s1", []string{"deposit.confirmed"})
	svc.CreateConfig(ctx, "https://b.com/wh", "s2", []string{"withdrawal.confirmed"})

	configs, err := svc.ListConfigs(ctx)
	if err != nil {
		t.Fatalf("ListConfigs: %v", err)
	}
	if len(configs) != 2 {
		t.Errorf("expected 2, got %d", len(configs))
	}
}

func TestDeleteConfig(t *testing.T) {
	db := mocks.TestDB(t)
	svc := NewService(db, nil)
	ctx := context.Background()

	cfg, _ := svc.CreateConfig(ctx, "https://del.com/wh", "s", []string{"deposit.confirmed"})
	if err := svc.DeleteConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("DeleteConfig: %v", err)
	}

	configs, _ := svc.ListConfigs(ctx)
	if len(configs) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(configs))
	}
}

func TestDeliver_Success(t *testing.T) {
	db := mocks.TestDB(t)
	svc := NewService(db, nil)
	ctx := context.Background()

	// Setup test HTTP server
	var receivedBody string
	var receivedSig string
	var receivedEvent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		receivedSig = r.Header.Get("X-Vault-Signature")
		receivedEvent = r.Header.Get("X-Vault-Event")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create webhook config + dummy transaction
	w := mocks.InsertWallet(t, db, "eth")
	tx := mocks.InsertTransaction(t, db, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)

	// Insert webhook event manually
	payload := `{"type":"deposit.confirmed","data":{"amount":"100"}}`
	eventID := "evt-test-123"
	db.Exec(`INSERT INTO webhook_events (id, transaction_id, event_type, payload, delivery_url, delivery_status, attempts, max_attempts, created_at)
		VALUES ($1, $2, 'deposit.confirmed', $3, $4, 'pending', 0, 10, NOW())`,
		eventID, tx.ID, payload, server.URL)

	secret := "test-secret"
	msg := types.WebhookMessage{
		EventID:     eventID,
		EventType:   types.EventDepositConfirmed,
		Payload:     payload,
		DeliveryURL: server.URL,
		Secret:      secret,
		Attempt:     1,
	}

	err := svc.Deliver(ctx, msg)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// Verify payload received
	if receivedBody != payload {
		t.Errorf("payload mismatch: %s", receivedBody)
	}

	// Verify event header
	if receivedEvent != "deposit.confirmed" {
		t.Errorf("expected deposit.confirmed, got %s", receivedEvent)
	}

	// Verify HMAC signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if receivedSig != expectedSig {
		t.Errorf("HMAC mismatch: got %s, want %s", receivedSig, expectedSig)
	}

	// Verify status updated in DB
	var status string
	db.Get(&status, "SELECT delivery_status FROM webhook_events WHERE id = $1", eventID)
	if status != "delivered" {
		t.Errorf("expected delivered status, got %s", status)
	}
}

func TestDeliver_Failure(t *testing.T) {
	db := mocks.TestDB(t)
	svc := NewService(db, nil)
	ctx := context.Background()

	// Server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	w := mocks.InsertWallet(t, db, "eth")
	tx := mocks.InsertTransaction(t, db, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)

	eventID := "evt-fail-123"
	db.Exec(`INSERT INTO webhook_events (id, transaction_id, event_type, payload, delivery_url, delivery_status, attempts, max_attempts, created_at)
		VALUES ($1, $2, 'deposit.confirmed', '{}', $3, 'pending', 0, 10, NOW())`,
		eventID, tx.ID, server.URL)

	msg := types.WebhookMessage{
		EventID: eventID, Payload: "{}", DeliveryURL: server.URL, Secret: "s", Attempt: 1,
	}

	err := svc.Deliver(ctx, msg)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	// Verify attempt incremented
	var attempts int
	db.Get(&attempts, "SELECT attempts FROM webhook_events WHERE id = $1", eventID)
	if attempts != 1 {
		t.Errorf("expected attempts=1, got %d", attempts)
	}
}

func TestDeliver_Unreachable(t *testing.T) {
	db := mocks.TestDB(t)
	svc := NewService(db, nil)
	ctx := context.Background()

	w := mocks.InsertWallet(t, db, "eth")
	tx := mocks.InsertTransaction(t, db, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)

	eventID := "evt-unreach-123"
	db.Exec(`INSERT INTO webhook_events (id, transaction_id, event_type, payload, delivery_url, delivery_status, attempts, max_attempts, created_at)
		VALUES ($1, $2, 'deposit.confirmed', '{}', 'http://localhost:1/nope', 'pending', 0, 10, NOW())`,
		eventID, tx.ID)

	msg := types.WebhookMessage{
		EventID: eventID, Payload: "{}", DeliveryURL: "http://localhost:1/nope", Secret: "s",
	}

	err := svc.Deliver(ctx, msg)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestPgArray(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{[]string{"a", "b"}, `{"a","b"}`},
		{[]string{"deposit.confirmed"}, `{"deposit.confirmed"}`},
		{[]string{}, "{}"},
	}
	for _, tt := range tests {
		got := pgArray(tt.input)
		if got != tt.want {
			t.Errorf("pgArray(%v) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestEnqueueEvent_NoConfigs(t *testing.T) {
	db := mocks.TestDB(t)
	svc := NewService(db, nil)

	// Insert a wallet + transaction for FK
	w := mocks.InsertWallet(t, db, "eth")
	tx := mocks.InsertTransaction(t, db, w.ID, nil, "eth", "deposit", "pending", "eth", "100", 50)

	// Should not panic with no webhook configs
	svc.EnqueueEvent(context.Background(), tx.ID, types.EventDepositPending, map[string]string{"test": "data"})

	var count int
	db.Get(&count, "SELECT COUNT(*) FROM webhook_events")
	if count != 0 {
		t.Errorf("expected 0 events with no configs, got %d", count)
	}
}

func TestWebhookPayloadStructure(t *testing.T) {
	payload := map[string]interface{}{
		"id":   "test-id",
		"type": string(types.EventDepositConfirmed),
		"data": map[string]string{"tx_hash": "0xabc"},
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(bytes, &parsed)
	if parsed["type"] != "deposit.confirmed" {
		t.Errorf("expected deposit.confirmed, got %v", parsed["type"])
	}
}
