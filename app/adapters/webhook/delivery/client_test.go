package delivery

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// testGuard is the webhook policy with loopback allowed for the httptest receivers.
func testGuard() *egress.Guard {
	return egress.NewGuard(egress.Policy{AllowLoopback: true}, nil)
}

// pinnedResolver answers every name with one address, as a DNS record would.
type pinnedResolver struct{ addr netip.Addr }

func (r pinnedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{r.addr}, nil
}

func signedCall(url string) webhook.SignedDelivery {
	return webhook.SignedDelivery{
		URL:         url,
		Body:        []byte("{}"),
		Signature:   "sig",
		SignatureV2: "t=1760950000,v1=sig",
		Timestamp:   1760950000,
		EventType:   "deposit.confirmed",
		DeliveryID:  "delivery-1",
		Timeout:     time.Second,
	}
}

func TestPost_RefusesAnInternalDestinationBeforeAnyConnection(t *testing.T) {
	cases := map[string]string{
		"http://169.254.169.254/latest/meta-data/": egress.ReasonLinkLocal,
		"https://169.254.170.2/v2/credentials":     egress.ReasonLinkLocal,
		"https://10.0.0.1/hook":                    egress.ReasonPrivate,
		"http://172.17.0.1:8200/v1/sys/health":     egress.ReasonPrivate,
		"https://openbao:8200/v1/sys/health":       egress.ReasonHostName,
		"https://metadata.google.internal/":        egress.ReasonMetadata,
		"http://hooks.example.com/in":              egress.ReasonScheme,
		"https://user:pass@hooks.example.com/in":   egress.ReasonUserinfo,
	}
	for url, reason := range cases {
		_, err := NewClient(egress.NewGuard(egress.Policy{}, pinnedResolver{netip.MustParseAddr("93.184.216.34")})).Post(context.Background(), signedCall(url))
		if !errors.Is(err, webhook.ErrURLNotAllowed) || !httpclient.IsBuild(err) {
			t.Fatalf("%s: err = %v, want a not-sent refusal", url, err)
		}
		if got := webhook.RefusalReason(err); got != reason {
			t.Fatalf("%s: reason = %q, want %q", url, got, reason)
		}
	}
}

func TestPost_ConnectsOnlyToTheAddressTheNameResolvesToWhenItIsAllowed(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	// A name that was public when the webhook was registered and now resolves to
	// the loopback receiver (DNS rebinding).
	rebound := pinnedResolver{netip.MustParseAddr("127.0.0.1")}
	call := signedCall("http://hooks.example.test:" + port + "/in")

	_, err := NewClient(egress.NewGuard(egress.Policy{AllowHTTP: true}, rebound)).Post(context.Background(), call)
	if !errors.Is(err, webhook.ErrURLNotAllowed) || webhook.RefusalReason(err) != egress.ReasonLoopback || !httpclient.IsBuild(err) {
		t.Fatalf("err = %v, want the loopback refusal of the resolved address", err)
	}
	if received.Load() != 0 {
		t.Fatal("a refused address was posted to")
	}

	resp, err := NewClient(egress.NewGuard(egress.Policy{AllowHTTP: true, AllowLoopback: true}, rebound)).Post(context.Background(), call)
	if err != nil || resp.StatusCode != http.StatusNoContent || received.Load() != 1 {
		t.Fatalf("an allowed address: status %d, err %v, received %d", resp.StatusCode, err, received.Load())
	}
}

func TestPost_ReadsOnlyTheStartOfTheAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", 4*deliveryResponseMaxBytes)))
	}))
	t.Cleanup(server.Close)

	resp, err := NewClient(testGuard()).Post(context.Background(), signedCall(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(resp.Body) > deliveryResponseMaxBytes {
		t.Fatalf("status %d, read %d bytes, want at most %d", resp.StatusCode, len(resp.Body), deliveryResponseMaxBytes)
	}
}

func TestNew_Client_RequiresAGuard(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a client without a destination guard was built")
		}
	}()
	NewClient(nil)
}

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

	resp, err := NewClient(testGuard()).Post(context.Background(), webhook.SignedDelivery{
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

	if _, err := NewClient(testGuard()).Post(context.Background(), webhook.SignedDelivery{
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
	_, err := NewClient(testGuard()).Post(context.Background(), webhook.SignedDelivery{
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
	var noContext context.Context // a nil context is the case under test
	if _, err := NewClient(testGuard()).Post(noContext, call); !httpclient.IsBuild(err) || called {
		t.Fatal("a nil context was posted, or its refusal was not a not-sent (build) error")
	}
	withoutTimeout := call
	withoutTimeout.Timeout = 0
	if _, err := NewClient(testGuard()).Post(context.Background(), withoutTimeout); !httpclient.IsBuild(err) || called {
		t.Fatal("a delivery without a timeout was posted, or its refusal was not a not-sent (build) error")
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
	// A refusal here is a build error: the delivery never left, so the service does
	// not count it as an attempt.
	withoutTimestamp := call
	withoutTimestamp.Timestamp = 0
	if _, err := NewClient(testGuard()).Post(context.Background(), withoutTimestamp); !httpclient.IsBuild(err) || called {
		t.Fatal("a delivery without the time it was signed at was posted, or refused as a sent attempt")
	}
	withoutSignatureV2 := call
	withoutSignatureV2.SignatureV2 = ""
	if _, err := NewClient(testGuard()).Post(context.Background(), withoutSignatureV2); !httpclient.IsBuild(err) || called {
		t.Fatal("a delivery without X-Vault-Signature-V2 was posted, or refused as a sent attempt")
	}
}

func TestPost_DoesNotFollowARedirectWithTheSignatures(t *testing.T) {
	var forwarded http.Header
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirect.Close)

	resp, err := NewClient(testGuard()).Post(context.Background(), webhook.SignedDelivery{
		URL:         redirect.URL,
		Body:        []byte("{}"),
		Signature:   "sig",
		SignatureV2: "t=1760950000,v1=sig",
		Timestamp:   1760950000,
		EventType:   "deposit.confirmed",
		DeliveryID:  "delivery-1",
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want the 307 itself", resp.StatusCode)
	}
	if forwarded != nil {
		t.Fatal("the redirect was followed and the signed headers went to another URL")
	}
}
