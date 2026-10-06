package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// The delivery adapter imports this package, so these tests cannot import it.
// Register the same POST the adapter uses so Deliver signs with the loaded secret.
func init() {
	SetDeliveryClient(func() DeliveryClient { return testHTTPDelivery{} })
}

type testHTTPDelivery struct{}

func (testHTTPDelivery) Post(ctx context.Context, call SignedDelivery) (httpclient.Response, error) {
	if ctx == nil {
		return httpclient.Response{}, fmt.Errorf("webhook delivery: context is required")
	}
	if call.Timeout <= 0 {
		return httpclient.Response{}, fmt.Errorf("webhook delivery: timeout is required")
	}
	return httpclient.NewClient(call.Timeout).Do(ctx, httpclient.Request{
		Method: httpclient.MethodPost,
		URL:    call.URL,
		Header: map[string]string{
			"Content-Type":        "application/json",
			"X-Vault-Signature":   call.Signature,
			"X-Vault-Event":       call.EventType,
			"X-Vault-Delivery-Id": call.DeliveryID,
			"X-Vault-Timestamp":   fmt.Sprintf("%d", time.Now().Unix()),
		},
		Body:    call.Body,
		HasBody: true,
	})
}

func TestService_Create_Config(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	ctx := context.Background()

	cfg, err := svc.CreateConfig(ctx, "https://example.com/webhook", "secret123", []string{"deposit.confirmed", "withdrawal.confirmed"}, nil)
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

func TestService_List_Configs(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	ctx := context.Background()

	svc.CreateConfig(ctx, "https://a.com/wh", "s1", []string{"deposit.confirmed"}, nil)
	svc.CreateConfig(ctx, "https://b.com/wh", "s2", []string{"withdrawal.confirmed"}, nil)

	configs, err := svc.ListConfigs(ctx)
	if err != nil {
		t.Fatalf("ListConfigs: %v", err)
	}
	if len(configs) != 2 {
		t.Errorf("expected 2, got %d", len(configs))
	}
}

func TestService_Delete_Config(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	ctx := context.Background()

	cfg, _ := svc.CreateConfig(ctx, "https://del.com/wh", "s", []string{"deposit.confirmed"}, nil)
	if err := svc.DeleteConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("DeleteConfig: %v", err)
	}

	configs, _ := svc.ListConfigs(ctx)
	if len(configs) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(configs))
	}
}

func TestService_Deliver_Success(t *testing.T) {
	svc, store := newMemoryWebhookService()
	ctx := context.Background()

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

	payload := `{"type":"deposit.confirmed","data":{"amount":"100"}}`
	eventID := uuid.New()
	txID := uuid.New()
	if err := store.addEvent(&models.WebhookEvent{
		ID: eventID, TransactionID: &txID, EventType: "deposit.confirmed", Payload: payload,
		DeliveryURL: server.URL, DeliveryStatus: "pending", MaxAttempts: 10,
	}); err != nil {
		t.Fatal(err)
	}

	secret := "test-secret"
	cfg, err := svc.CreateConfig(ctx, server.URL, secret, []string{"deposit.confirmed"}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	msg := types.WebhookMessage{
		EventID:     eventID.String(),
		EventType:   types.EventDepositConfirmed,
		Payload:     payload,
		DeliveryURL: server.URL,
		ConfigID:    cfg.ID.String(),
		Attempt:     1,
	}

	err = svc.Deliver(ctx, msg)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	if receivedBody != payload {
		t.Errorf("payload mismatch: %s", receivedBody)
	}
	if receivedEvent != "deposit.confirmed" {
		t.Errorf("expected deposit.confirmed, got %s", receivedEvent)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if receivedSig != expectedSig {
		t.Errorf("HMAC mismatch: got %s, want %s", receivedSig, expectedSig)
	}

	event := store.listEvents()[0]
	if event.DeliveryStatus != "delivered" {
		t.Errorf("expected delivered status, got %s", event.DeliveryStatus)
	}
}

func TestDeliver_Redelivery_DoesNotSend(t *testing.T) {
	svc, store := newMemoryWebhookService()
	ctx := context.Background()

	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	payload := `{"type":"deposit.confirmed","data":{"amount":"100"}}`
	eventID := uuid.New()
	txID := uuid.New()
	if err := store.addEvent(&models.WebhookEvent{
		ID: eventID, TransactionID: &txID, EventType: "deposit.confirmed", Payload: payload,
		DeliveryURL: server.URL, DeliveryStatus: "pending", MaxAttempts: 10,
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := svc.CreateConfig(ctx, server.URL, "redelivery-secret", []string{"deposit.confirmed"}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	msg := types.WebhookMessage{
		EventID:     eventID.String(),
		EventType:   types.EventDepositConfirmed,
		Payload:     payload,
		DeliveryURL: server.URL,
		ConfigID:    cfg.ID.String(),
		Attempt:     1,
	}

	if err := svc.Deliver(ctx, msg); err != nil {
		t.Fatalf("first Deliver: %v", err)
	}
	if err := svc.Deliver(ctx, msg); err != nil {
		t.Fatalf("redelivery Deliver: %v", err)
	}
	if posts != 1 {
		t.Fatalf("redelivery posted %d times, want 1", posts)
	}

	event := store.listEvents()[0]
	if event.DeliveryStatus != models.WebhookDeliveryDelivered {
		t.Fatalf("status = %s, want delivered", event.DeliveryStatus)
	}
	if event.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", event.Attempts)
	}
}

func TestService_Deliver_Failure(t *testing.T) {
	svc, store := newMemoryWebhookService()
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	eventID := uuid.New()
	txID := uuid.New()
	if err := store.addEvent(&models.WebhookEvent{
		ID: eventID, TransactionID: &txID, EventType: "deposit.confirmed", Payload: "{}",
		DeliveryURL: server.URL, DeliveryStatus: "pending", MaxAttempts: 10,
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := svc.CreateConfig(ctx, server.URL, "s", []string{"deposit.confirmed"}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	msg := types.WebhookMessage{
		EventID: eventID.String(), Payload: "{}", DeliveryURL: server.URL, ConfigID: cfg.ID.String(), Attempt: 1,
	}

	err = svc.Deliver(ctx, msg)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	event := store.listEvents()[0]
	if event.Attempts != 1 {
		t.Errorf("expected attempts=1, got %d", event.Attempts)
	}
}

func TestService_Deliver_Unreachable(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	ctx := context.Background()

	cfg, err := svc.CreateConfig(ctx, "http://localhost:1/nope", "s", []string{"deposit.confirmed"}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	msg := types.WebhookMessage{
		EventID: uuid.NewString(), Payload: "{}", DeliveryURL: "http://localhost:1/nope", ConfigID: cfg.ID.String(),
	}

	err = svc.Deliver(ctx, msg)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestEnqueue_Event_QueuePayloadCarriesNoSecret(t *testing.T) {
	ctx := context.Background()
	const secret = "queue-payload-must-not-carry-this-secret"
	var messages []types.WebhookMessage
	sender := mocks.NewMockSender(t)
	sender.EXPECT().SendWebhook(mock.Anything, mock.Anything).Run(func(_ context.Context, msg types.WebhookMessage) {
		messages = append(messages, msg)
	}).Return(nil).Maybe()
	store := newMemoryWebhook()
	svc := NewService(Deps{
		SQS:     sender,
		Configs: store,
		Events:  memoryEvents{store: store},
	})
	events := []string{string(types.EventDepositPending), string(types.EventWithdrawalBroadcasting)}
	cfg, err := svc.CreateConfig(ctx, "https://example.com/hook", secret, events, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	tx := models.Transaction{ID: uuid.New(), WalletID: uuid.New(), Chain: "eth", TxType: "deposit", Status: "pending", Asset: "eth", Amount: "100"}

	svc.EnqueueEvent(ctx, tx.ID, types.EventDepositPending, map[string]string{"test": "data"})
	if _, err := svc.EnqueueScoped(ctx, ScopedEvent{
		EventType: types.EventDepositPending,
		SubjectID: uuid.NewString(),
		WalletID:  tx.WalletID,
		Data:      map[string]string{"test": "data"},
	}); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	send, err := svc.StageWithdrawalBroadcasting(ctx, &tx)
	if err != nil {
		t.Fatalf("StageWithdrawalBroadcasting: %v", err)
	}
	if send == nil {
		t.Fatal("expected a queued withdrawal webhook")
	}
	send(ctx)

	if len(messages) != 3 {
		t.Fatalf("queued messages = %d, want 3", len(messages))
	}
	for _, msg := range messages {
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), `"secret"`) {
			t.Fatal("queue payload carries a secret")
		}
		if msg.ConfigID != cfg.ID.String() {
			t.Fatalf("config id = %s", msg.ConfigID)
		}
	}
}

func TestService_Pg_Array(t *testing.T) {
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

func TestEnqueue_Event_NoConfigs(t *testing.T) {
	svc, store := newMemoryWebhookService()
	txID := uuid.New()

	svc.EnqueueEvent(context.Background(), txID, types.EventDepositPending, map[string]string{"test": "data"})

	if count := len(store.listEvents()); count != 0 {
		t.Errorf("expected 0 events with no configs, got %d", count)
	}
}

func TestWebhook_Payload_Structure(t *testing.T) {
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
