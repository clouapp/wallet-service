package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const signatureSecret = "signature-test-secret"

// signedRequest is what a receiver got on one delivery.
type signedRequest struct {
	body      string
	legacy    string
	v2        string
	timestamp string
}

type signatureReceiver struct {
	mu       sync.Mutex
	status   int
	requests []signedRequest
}

func (r *signatureReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, signedRequest{
		body:      string(body),
		legacy:    req.Header.Get("X-Vault-Signature"),
		v2:        req.Header.Get("X-Vault-Signature-V2"),
		timestamp: req.Header.Get("X-Vault-Timestamp"),
	})
	w.WriteHeader(r.status)
}

func (r *signatureReceiver) last(t *testing.T) signedRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		t.Fatal("the receiver got no delivery")
	}
	return r.requests[len(r.requests)-1]
}

func hmacHexOf(secret, message string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// assertSignedAt checks both signatures of one delivery and that it was signed
// between before and after (unix seconds), the window of the call that sent it.
func assertSignedAt(t *testing.T, got signedRequest, secret string, before, after int64) int64 {
	t.Helper()
	if got.legacy != hmacHexOf(secret, got.body) {
		t.Fatal("X-Vault-Signature must stay the HMAC-SHA256 of the raw body during the rollout")
	}
	timestamp, err := strconv.ParseInt(got.timestamp, 10, 64)
	if err != nil {
		t.Fatalf("X-Vault-Timestamp %q is not unix seconds", got.timestamp)
	}
	if timestamp < before || timestamp > after {
		t.Fatalf("X-Vault-Timestamp = %d, want the send time, between %d and %d", timestamp, before, after)
	}
	want := "t=" + got.timestamp + ",v1=" + hmacHexOf(secret, got.timestamp+"."+got.body)
	if got.v2 != want {
		t.Fatalf("X-Vault-Signature-V2 = %q, want %q (HMAC-SHA256 of \"<t>.<body>\")", got.v2, want)
	}
	return timestamp
}

func TestDeliver_Signature_SignsTheTimestampAndTheBodyInSignatureV2(t *testing.T) {
	svc, store := newMemoryWebhookService()
	receiver := &signatureReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg, err := svc.CreateConfig(context.Background(), server.URL, signatureSecret, []string{string(types.EventDepositConfirmed)}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	eventID := uuid.New()
	payload := `{"type":"deposit.confirmed","data":{"amount":"1.5"}}`
	if addErr := store.addEvent(&models.WebhookEvent{ID: eventID, WebhookConfigID: &cfg.ID, EventType: string(types.EventDepositConfirmed), Payload: payload, DeliveryURL: server.URL, MaxAttempts: 10}); addErr != nil {
		t.Fatal(addErr)
	}

	before := time.Now().Unix()
	err = svc.Deliver(context.Background(), types.WebhookMessage{
		EventID: eventID.String(), EventType: types.EventDepositConfirmed, Payload: payload,
		DeliveryURL: server.URL, ConfigID: cfg.ID.String(), Attempt: 1,
	})
	after := time.Now().Unix()
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	got := receiver.last(t)
	if got.body != payload {
		t.Fatal("the signed body must be the stored payload, byte for byte")
	}
	assertSignedAt(t, got, signatureSecret, before, after)
}

func TestDeliver_Signature_EachAttemptIsSignedWithItsOwnSendTime(t *testing.T) {
	store := newMemoryWebhook()
	sendTimes := []int64{1760950000, 1760950042}
	var clockReads int
	clock := func() time.Time {
		at := sendTimes[min(clockReads, len(sendTimes)-1)]
		clockReads++
		return time.Unix(at, 0)
	}
	svc := NewService(Deps{Configs: store, Events: memoryEvents{store: store}, Now: clock, Destinations: testDestinations()})
	receiver := &signatureReceiver{status: http.StatusServiceUnavailable}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg, err := svc.CreateConfig(context.Background(), server.URL, signatureSecret, []string{string(types.EventDepositConfirmed)}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	eventID := uuid.New()
	payload := `{"type":"deposit.confirmed"}`
	if addErr := store.addEvent(&models.WebhookEvent{ID: eventID, WebhookConfigID: &cfg.ID, EventType: string(types.EventDepositConfirmed), Payload: payload, DeliveryURL: server.URL, MaxAttempts: 10}); addErr != nil {
		t.Fatal(addErr)
	}
	msg := types.WebhookMessage{
		EventID: eventID.String(), EventType: types.EventDepositConfirmed, Payload: payload,
		DeliveryURL: server.URL, ConfigID: cfg.ID.String(), Attempt: 1,
	}

	if err := svc.Deliver(context.Background(), msg); err == nil {
		t.Fatal("a 503 must fail the attempt so the queue retries it")
	}
	receiver.status = http.StatusOK
	msg.Attempt = 2
	if err := svc.Deliver(context.Background(), msg); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if len(receiver.requests) != 2 {
		t.Fatalf("receiver got %d deliveries, want 2", len(receiver.requests))
	}
	first := assertSignedAt(t, receiver.requests[0], signatureSecret, sendTimes[0], sendTimes[0])
	retry := assertSignedAt(t, receiver.requests[1], signatureSecret, sendTimes[1], sendTimes[1])
	if first == retry || receiver.requests[0].v2 == receiver.requests[1].v2 {
		t.Fatal("the retry reused the first attempt's timestamp and signature")
	}
}

func TestDeliver_Redirect_IsAFailedAttemptNotADelivery(t *testing.T) {
	svc, store := newMemoryWebhookService()
	receiver := &signatureReceiver{status: http.StatusFound}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg, err := svc.CreateConfig(context.Background(), server.URL, signatureSecret, []string{string(types.EventDepositConfirmed)}, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	eventID := uuid.New()
	payload := `{"type":"deposit.confirmed"}`
	if addErr := store.addEvent(&models.WebhookEvent{ID: eventID, WebhookConfigID: &cfg.ID, EventType: string(types.EventDepositConfirmed), Payload: payload, DeliveryURL: server.URL, MaxAttempts: 10}); addErr != nil {
		t.Fatal(addErr)
	}

	err = svc.Deliver(context.Background(), types.WebhookMessage{
		EventID: eventID.String(), EventType: types.EventDepositConfirmed, Payload: payload,
		DeliveryURL: server.URL, ConfigID: cfg.ID.String(), Attempt: 1,
	})
	if err == nil {
		t.Fatal("a 3xx answer must fail the delivery so it is retried")
	}
	stored := store.listEvents()[0]
	if stored.DeliveryStatus != models.WebhookDeliveryPending || stored.Attempts != 1 {
		t.Fatalf("event = %s with %d attempts; want pending with the attempt counted", stored.DeliveryStatus, stored.Attempts)
	}
}

func TestSendTest_Redirect_IsAFailure(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	receiver := &signatureReceiver{status: http.StatusMovedPermanently}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: server.URL, Secret: signatureSecret, IsActive: true}

	if err := svc.SendTest(context.Background(), cfg, uuid.New()); err == nil {
		t.Fatal("a 3xx answer to webhook.test must be reported as a failure")
	}
}

func TestRedeliver_Signature_AnOldEventIsSignedWithTheTimeOfTheResend(t *testing.T) {
	svc, store := newMemoryWebhookService()
	receiver := &signatureReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	accountID := uuid.New()
	cfg, err := svc.CreateConfig(context.Background(), server.URL, signatureSecret, []string{string(types.EventDepositConfirmed)}, &accountID)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	created := time.Now().UTC().Add(-2 * time.Hour)
	eventID := uuid.New()
	deliveredAt := created
	if addErr := store.addEvent(&models.WebhookEvent{
		ID: eventID, WebhookConfigID: &cfg.ID, EventType: string(types.EventDepositConfirmed),
		Payload:     `{"type":"deposit.confirmed","created_at":"` + created.Format(time.RFC3339) + `"}`,
		DeliveryURL: server.URL, DeliveryStatus: models.WebhookDeliveryDelivered, Attempts: 1, MaxAttempts: 10, DeliveredAt: &deliveredAt,
	}); addErr != nil {
		t.Fatal(addErr)
	}
	store.backdate(eventID.String(), 2*time.Hour)
	store.ownEvent(eventID, accountID)

	before := time.Now().Unix()
	requeued, err := svc.Redeliver(context.Background(), accountID, cfg.ID, RedeliverySelection{EventIDs: []uuid.UUID{eventID}})
	if err != nil || requeued != 1 {
		t.Fatalf("Redeliver = %d, %v", requeued, err)
	}
	if delivered, err := svc.DeliverPending(context.Background(), deliveryBatchSize); err != nil || delivered != 1 {
		t.Fatalf("DeliverPending = %d, %v", delivered, err)
	}
	after := time.Now().Unix()

	signedAt := assertSignedAt(t, receiver.last(t), signatureSecret, before, after)
	if signedAt <= created.Unix() {
		t.Fatal("a redelivery must be signed with the time it is sent, not the time the event was created")
	}
}

func TestSendTest_Signature_SignsTheTestDeliveryLikeAnyOther(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	receiver := &signatureReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: server.URL, Secret: signatureSecret, IsActive: true}

	before := time.Now().Unix()
	if err := svc.SendTest(context.Background(), cfg, uuid.New()); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	after := time.Now().Unix()

	assertSignedAt(t, receiver.last(t), signatureSecret, before, after)
}

func TestSign_Delivery_NeverPutsTheSecretInEitherSignature(t *testing.T) {
	legacy, v2 := signDelivery(signatureSecret, []byte(`{"id":"1"}`), 1760950000)
	for _, value := range []string{legacy, v2} {
		if value == "" || strings.Contains(value, signatureSecret) {
			t.Fatalf("signature %q is empty or carries the secret", value)
		}
	}
	if v2 != "t=1760950000,v1="+hmacHexOf(signatureSecret, `1760950000.{"id":"1"}`) {
		t.Fatalf("X-Vault-Signature-V2 = %q", v2)
	}
}

// The worked example of docs/vault-summary-v2.md ("Assinatura das entregas de
// webhook"): receivers check their implementation against it.
func TestSign_Delivery_MatchesTheDocumentedExample(t *testing.T) {
	legacy, v2 := signDelivery("whsec_example", []byte(`{"id":"5b0c4c1e-0000-4000-8000-000000000001","type":"webhook.test"}`), 1760950000)
	if v2 != "t=1760950000,v1=b992741a9a5db088608c3ae42174ecba53233aa8a11298699d75ea7c3ec29a8e" {
		t.Fatalf("X-Vault-Signature-V2 = %q, the documented example no longer holds", v2)
	}
	if legacy != "74afff9997fc16d8758aabf679d5e10bc6810a36a9755b6986b7ecc5d48b74c2" {
		t.Fatalf("X-Vault-Signature = %q, the documented example no longer holds", legacy)
	}
}
