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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
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

func TestMain(m *testing.M) {
	// Boot Goravel once for all tests in this package
	testutil.BootTest()
	os.Exit(m.Run())
}

func newTestWebhookSvc() *Service {
	return NewService(Deps{
		Configs: repositories.NewWebhookConfigRepository(repositories.WebhookConfigRepositoryDeps{
			Cipher: facades.Crypt(),
		}),
		Events: repositories.NewWebhookEventRepository(nil),
	})
}

func TestCreateConfig(t *testing.T) {
	fixtures.TestDB(t)
	svc := newTestWebhookSvc()
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

func TestListConfigs(t *testing.T) {
	fixtures.TestDB(t)
	svc := newTestWebhookSvc()
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

func TestDeleteConfig(t *testing.T) {
	fixtures.TestDB(t)
	svc := newTestWebhookSvc()
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

func TestDeliver_Success(t *testing.T) {
	fixtures.TestDB(t)
	svc := newTestWebhookSvc()
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
	w := fixtures.InsertWallet(t, "eth")
	tx := fixtures.InsertTransaction(t, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)

	// Insert webhook event manually
	payload := `{"type":"deposit.confirmed","data":{"amount":"100"}}`
	eventID := uuid.NewString()
	facades.Orm().Query().Exec(`INSERT INTO webhook_events (id, transaction_id, event_type, payload, delivery_url, delivery_status, attempts, max_attempts, created_at)
		VALUES ($1, $2, 'deposit.confirmed', $3, $4, 'pending', 0, 10, NOW())`,
		eventID, tx.ID, payload, server.URL)

	secret := "test-secret"
	cfg, err := svc.CreateConfig(ctx, server.URL, secret, []string{"deposit.confirmed"}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	msg := types.WebhookMessage{
		EventID:     eventID,
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
	var event models.WebhookEvent
	if err := facades.Orm().Query().Where("id", eventID).First(&event); err != nil {
		t.Fatalf("find webhook event: %v", err)
	}
	if event.DeliveryStatus != "delivered" {
		t.Errorf("expected delivered status, got %s", event.DeliveryStatus)
	}
}

func TestDeliver_RedeliveryDoesNotSend(t *testing.T) {
	fixtures.TestDB(t)
	svc := newTestWebhookSvc()
	ctx := context.Background()

	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w := fixtures.InsertWallet(t, "eth")
	tx := fixtures.InsertTransaction(t, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)
	payload := `{"type":"deposit.confirmed","data":{"amount":"100"}}`
	eventID := uuid.NewString()
	facades.Orm().Query().Exec(`INSERT INTO webhook_events (id, transaction_id, event_type, payload, delivery_url, delivery_status, attempts, max_attempts, created_at)
		VALUES ($1, $2, 'deposit.confirmed', $3, $4, 'pending', 0, 10, NOW())`,
		eventID, tx.ID, payload, server.URL)

	cfg, err := svc.CreateConfig(ctx, server.URL, "redelivery-secret", []string{"deposit.confirmed"}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	msg := types.WebhookMessage{
		EventID:     eventID,
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

	var event models.WebhookEvent
	if err := facades.Orm().Query().Where("id", eventID).First(&event); err != nil {
		t.Fatalf("find webhook event: %v", err)
	}
	if event.DeliveryStatus != models.WebhookDeliveryDelivered {
		t.Fatalf("status = %s, want delivered", event.DeliveryStatus)
	}
	if event.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", event.Attempts)
	}
}

func TestDeliver_Failure(t *testing.T) {
	fixtures.TestDB(t)
	svc := newTestWebhookSvc()
	ctx := context.Background()

	// Server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	w := fixtures.InsertWallet(t, "eth")
	tx := fixtures.InsertTransaction(t, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)

	eventID := uuid.NewString()
	facades.Orm().Query().Exec(`INSERT INTO webhook_events (id, transaction_id, event_type, payload, delivery_url, delivery_status, attempts, max_attempts, created_at)
		VALUES ($1, $2, 'deposit.confirmed', '{}', $3, 'pending', 0, 10, NOW())`,
		eventID, tx.ID, server.URL)

	cfg, err := svc.CreateConfig(ctx, server.URL, "s", []string{"deposit.confirmed"}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	msg := types.WebhookMessage{
		EventID: eventID, Payload: "{}", DeliveryURL: server.URL, ConfigID: cfg.ID.String(), Attempt: 1,
	}

	err = svc.Deliver(ctx, msg)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	// Verify attempt incremented
	var event models.WebhookEvent
	if err := facades.Orm().Query().Where("id", eventID).First(&event); err != nil {
		t.Fatalf("find webhook event: %v", err)
	}
	if event.Attempts != 1 {
		t.Errorf("expected attempts=1, got %d", event.Attempts)
	}
}

func TestDeliver_Unreachable(t *testing.T) {
	fixtures.TestDB(t)
	svc := newTestWebhookSvc()
	ctx := context.Background()

	w := fixtures.InsertWallet(t, "eth")
	tx := fixtures.InsertTransaction(t, w.ID, nil, "eth", "deposit", "confirmed", "eth", "100", 50)

	eventID := "evt-unreach-123"
	facades.Orm().Query().Exec(`INSERT INTO webhook_events (id, transaction_id, event_type, payload, delivery_url, delivery_status, attempts, max_attempts, created_at)
		VALUES ($1, $2, 'deposit.confirmed', '{}', 'http://localhost:1/nope', 'pending', 0, 10, NOW())`,
		eventID, tx.ID)

	cfg, err := svc.CreateConfig(ctx, "http://localhost:1/nope", "s", []string{"deposit.confirmed"}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	msg := types.WebhookMessage{
		EventID: eventID, Payload: "{}", DeliveryURL: "http://localhost:1/nope", ConfigID: cfg.ID.String(),
	}

	err = svc.Deliver(ctx, msg)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

type recordingWebhookSender struct {
	messages []types.WebhookMessage
}

func (r *recordingWebhookSender) SendWebhook(_ context.Context, msg types.WebhookMessage) error {
	r.messages = append(r.messages, msg)
	return nil
}

func TestEnqueueEvent_QueuePayloadCarriesNoSecret(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	const secret = "queue-payload-must-not-carry-this-secret"
	sender := &recordingWebhookSender{}
	svc := NewService(Deps{
		SQS: sender,
		Configs: repositories.NewWebhookConfigRepository(repositories.WebhookConfigRepositoryDeps{
			Cipher: facades.Crypt(),
		}),
		Events: repositories.NewWebhookEventRepository(nil),
	})
	events := []string{string(types.EventDepositPending), string(types.EventWithdrawalBroadcasting)}
	cfg, err := svc.CreateConfig(ctx, "https://example.com/hook", secret, events, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	w := fixtures.InsertWallet(t, "eth")
	tx := fixtures.InsertTransaction(t, w.ID, nil, "eth", "deposit", "pending", "eth", "100", 50)

	svc.EnqueueEvent(ctx, tx.ID, types.EventDepositPending, map[string]string{"test": "data"})
	if _, err := svc.EnqueueScoped(ctx, ScopedEvent{
		EventType: types.EventDepositPending,
		SubjectID: uuid.NewString(),
		WalletID:  w.ID,
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

	if len(sender.messages) != 3 {
		t.Fatalf("queued messages = %d, want 3", len(sender.messages))
	}
	for _, msg := range sender.messages {
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
	fixtures.TestDB(t)
	svc := newTestWebhookSvc()

	// Insert a wallet + transaction for FK
	w := fixtures.InsertWallet(t, "eth")
	tx := fixtures.InsertTransaction(t, w.ID, nil, "eth", "deposit", "pending", "eth", "100", 50)

	// Should not panic with no webhook configs
	svc.EnqueueEvent(context.Background(), tx.ID, types.EventDepositPending, map[string]string{"test": "data"})

	count, err := facades.Orm().Query().Model(&models.WebhookEvent{}).Count()
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
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
