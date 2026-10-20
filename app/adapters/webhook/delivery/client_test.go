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

func TestPost_Sends_TheSignedBodyUnchanged(t *testing.T) {
	const (
		signature   = "already-signed"
		signatureV2 = "t=1760950000,v1=already-signed-v2"
		signedAt    = int64(1760950000)
		eventType   = "deposit.confirmed"
		eventID     = "delivery-1"
		body        = `{"id":"delivery-1","type":"deposit.confirmed"}`
	)
	var (
		method  string
		gotBody string
		headers http.Header
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		payload, _ := io.ReadAll(r.Body)
		gotBody = string(payload)
		headers = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	resp, err := NewClient().Post(context.Background(), webhook.SignedDelivery{
		URL:         server.URL,
		Body:        []byte(body),
		Signature:   signature,
		SignatureV2: signatureV2,
		Timestamp:   signedAt,
		EventType:   eventType,
		DeliveryID:  eventID,
		Timeout:     time.Second,
	})
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
		headers.Get("X-Vault-Signature-V2") != signatureV2 ||
		headers.Get("X-Vault-Event") != eventType ||
		headers.Get("X-Vault-Delivery-Id") != eventID {
		t.Fatal("webhook headers changed")
	}
	if headers.Get("X-Vault-Timestamp") != strconv.FormatInt(signedAt, 10) {
		t.Fatal("X-Vault-Timestamp must be the time the service signed, the t of X-Vault-Signature-V2")
	}
}

func TestPost_Keeps_RawBytesAndLeavesTheSecretOffTheWire(t *testing.T) {
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
		URL:         server.URL,
		Body:        raw,
		Signature:   "sig-only",
		SignatureV2: "t=1760950000,v1=sig-only",
		Timestamp:   1760950000,
		EventType:   "webhook.test",
		DeliveryID:  "raw-1",
		Timeout:     time.Second,
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

func TestPost_Omits_TheSignatureFromTransportErrors(t *testing.T) {
	const (
		signature   = "sig-not-logged"
		signatureV2 = "t=1760950000,v1=sig-v2-not-logged"
	)
	_, err := NewClient().Post(context.Background(), webhook.SignedDelivery{
		URL:         "http://127.0.0.1:1/nope",
		Body:        []byte("{}"),
		Signature:   signature,
		SignatureV2: signatureV2,
		Timestamp:   1760950000,
		EventType:   "webhook.test",
		DeliveryID:  "missing-1",
		Timeout:     200 * time.Millisecond,
	})
	if err == nil || strings.Contains(err.Error(), signature) || strings.Contains(err.Error(), signatureV2) {
		t.Fatal("transport error was accepted or included the signature")
	}
}

func TestPost_Requires_AContextAndATimeout(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	call := webhook.SignedDelivery{
		URL:         server.URL,
		Body:        []byte("{}"),
		Signature:   "sig",
		SignatureV2: "t=1760950000,v1=sig",
		Timestamp:   1760950000,
		EventType:   "webhook.test",
		DeliveryID:  "delivery-1",
		Timeout:     time.Second,
	}
	if _, err := NewClient().Post(nil, call); err == nil || called {
		t.Fatal("a nil context was posted")
	}
	withoutTimeout := call
	withoutTimeout.Timeout = 0
	if _, err := NewClient().Post(context.Background(), withoutTimeout); err == nil || called {
		t.Fatal("a delivery without a timeout was posted")
	}
}

func TestPost_Requires_TheTimestampedSignature(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	call := webhook.SignedDelivery{
		URL:         server.URL,
		Body:        []byte("{}"),
		Signature:   "sig",
		SignatureV2: "t=1760950000,v1=sig",
		Timestamp:   1760950000,
		EventType:   "webhook.test",
		DeliveryID:  "delivery-1",
		Timeout:     time.Second,
	}
	withoutTimestamp := call
	withoutTimestamp.Timestamp = 0
	if _, err := NewClient().Post(context.Background(), withoutTimestamp); err == nil || called {
		t.Fatal("a delivery without the time it was signed at was posted")
	}
	withoutSignatureV2 := call
	withoutSignatureV2.SignatureV2 = ""
	if _, err := NewClient().Post(context.Background(), withoutSignatureV2); err == nil || called {
		t.Fatal("a delivery without X-Vault-Signature-V2 was posted")
	}
}
