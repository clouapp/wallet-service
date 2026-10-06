package delivery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/webhook"
)

func TestPostSendsTheSignedBodyUnchanged(t *testing.T) {
	const (
		signature = "already-signed"
		eventType = "deposit.confirmed"
		eventID   = "delivery-1"
		body      = `{"id":"delivery-1","type":"deposit.confirmed"}`
	)
	var (
		method    string
		gotBody   string
		headers   http.Header
		timestamp string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		payload, _ := io.ReadAll(r.Body)
		gotBody = string(payload)
		headers = r.Header.Clone()
		timestamp = r.Header.Get("X-Vault-Timestamp")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	before := time.Now().Unix()
	resp, err := NewClient().Post(context.Background(), webhook.SignedDelivery{
		URL:        server.URL,
		Body:       []byte(body),
		Signature:  signature,
		EventType:  eventType,
		DeliveryID: eventID,
		Timeout:    time.Second,
	})
	after := time.Now().Unix()
	if err != nil {
		t.Fatal("signed post failed")
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if method != http.MethodPost || gotBody != body {
		t.Fatal("method or body changed")
	}
	if headers.Get("Content-Type") != "application/json" ||
		headers.Get("X-Vault-Signature") != signature ||
		headers.Get("X-Vault-Event") != eventType ||
		headers.Get("X-Vault-Delivery-Id") != eventID {
		t.Fatal("webhook headers changed")
	}
	unix, convErr := strconv.ParseInt(timestamp, 10, 64)
	if convErr != nil || unix < before || unix > after {
		t.Fatal("X-Vault-Timestamp was not the unix time of the post")
	}
}

func TestPostKeepsRawBytesAndLeavesTheSecretOffTheWire(t *testing.T) {
	const plantedSecret = "signing-secret-not-sent"
	raw := []byte{0x00, 0x01, '{', '}', 0xff}
	var got []byte
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		headers = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	if _, err := NewClient().Post(context.Background(), webhook.SignedDelivery{
		URL:        server.URL,
		Body:       raw,
		Signature:  "sig-only",
		EventType:  "webhook.test",
		DeliveryID: "raw-1",
		Timeout:    time.Second,
	}); err != nil {
		t.Fatal("raw post failed")
	}
	if string(got) != string(raw) {
		t.Fatal("raw body was rewritten")
	}
	for name, values := range headers {
		for _, value := range values {
			if strings.Contains(name, plantedSecret) || strings.Contains(value, plantedSecret) {
				t.Fatal("a signing secret was sent")
			}
		}
	}
	if strings.Contains(string(got), plantedSecret) {
		t.Fatal("a signing secret was written into the body")
	}
}

func TestPostOmitsTheSignatureFromTransportErrors(t *testing.T) {
	const signature = "sig-not-logged"
	_, err := NewClient().Post(context.Background(), webhook.SignedDelivery{
		URL:        "http://127.0.0.1:1/nope",
		Body:       []byte("{}"),
		Signature:  signature,
		EventType:  "webhook.test",
		DeliveryID: "missing-1",
		Timeout:    200 * time.Millisecond,
	})
	if err == nil || strings.Contains(err.Error(), signature) {
		t.Fatal("transport error was accepted or included the signature")
	}
}

func TestPostRequiresAContextAndATimeout(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	call := webhook.SignedDelivery{
		URL:        server.URL,
		Body:       []byte("{}"),
		Signature:  "sig",
		EventType:  "webhook.test",
		DeliveryID: "delivery-1",
		Timeout:    time.Second,
	}
	if _, err := NewClient().Post(nil, call); err == nil || called {
		t.Fatal("a nil context was posted")
	}
	call.Timeout = 0
	if _, err := NewClient().Post(context.Background(), call); err == nil || called {
		t.Fatal("a delivery without a timeout was posted")
	}
}
