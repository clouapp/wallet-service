package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingDial dials target whatever address it is asked for, and records the
// addresses.
type recordingDial struct {
	mu        sync.Mutex
	target    string
	addresses []string
	err       error
}

func (d *recordingDial) dial(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addresses = append(d.addresses, address)
	d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, d.target)
}

func (d *recordingDial) asked() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.addresses...)
}

func TestDirectTransport_OpensEveryConnectionThroughItsDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "hooks.example.test:8080" {
			t.Errorf("Host = %q", r.Host)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	dial := &recordingDial{target: server.Listener.Addr().String()}

	resp, err := NewDirectTransport(dial.dial).ClientWithoutRedirects(time.Second).Do(context.Background(), Request{
		Method: MethodPost, URL: "http://hooks.example.test:8080/in", Body: []byte("{}"), HasBody: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if asked := dial.asked(); len(asked) != 1 || asked[0] != "hooks.example.test:8080" {
		t.Fatalf("dialed %v, want the URL's host and port", asked)
	}
}

// The proxy of the environment (HTTP_PROXY, read once per process) would make the
// dial function see the proxy instead of the destination, so the transport has none.
func TestDirectTransport_NeverGoesThroughAProxy(t *testing.T) {
	transport := NewDirectTransport((&net.Dialer{}).DialContext)
	if transport.raw.Proxy != nil {
		t.Fatal("the direct transport reads a proxy")
	}
	if transport.raw.DialContext == nil {
		t.Fatal("the direct transport does not dial through its function")
	}
}

func TestDirectTransport_HandsBackARedirectInsteadOfFollowingIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			t.Error("the redirect was followed")
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	dial := &recordingDial{target: server.Listener.Addr().String()}

	resp, err := NewDirectTransport(dial.dial).ClientWithoutRedirects(time.Second).Do(context.Background(), Request{
		Method: MethodPost, URL: "http://hooks.example.test/in", HasBody: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want the 302 itself", resp.StatusCode)
	}
}

func TestDirectTransport_KeepsTheDialRefusalOnTheErrorChain(t *testing.T) {
	refused := errors.New("destination refused")
	dial := &recordingDial{err: refused}

	_, err := NewDirectTransport(dial.dial).ClientWithoutRedirects(time.Second).Do(context.Background(), Request{
		Method: MethodPost, URL: "http://hooks.example.test/in?token=secret", HasBody: true,
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the dial refusal on the chain", err)
	}
	if !IsRoundtrip(err) {
		t.Fatalf("err = %v, want a roundtrip failure", err)
	}
	if got := err.Error(); got == "" || strings.Contains(got, "token=secret") || strings.Contains(got, "hooks.example.test/in") {
		t.Fatalf("the error %q carries the URL", got)
	}
}

func TestGuardConnections_OpensEveryConnectionOfTheSharedTransportThroughTheDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	dial := &recordingDial{target: server.Listener.Addr().String()}
	GuardConnections(dial.dial)
	t.Cleanup(func() { GuardConnections(nil) })

	resp, err := NewClient(time.Second).Do(context.Background(), Request{Method: MethodGet, URL: "http://rpc.example.test:8545/"})
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("Do = %d, %v", resp.StatusCode, err)
	}
	if asked := dial.asked(); len(asked) != 1 || asked[0] != "rpc.example.test:8545" {
		t.Fatalf("dialed %v, want the request's host through the guard", asked)
	}

	refused := errors.New("destination refused")
	GuardConnections((&recordingDial{err: refused}).dial)
	if _, err := NewClient(time.Second).Do(context.Background(), Request{Method: MethodGet, URL: "http://other.example.test:8545/"}); !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the guard's refusal", err)
	}
}

func TestGuardConnections_WithoutAGuardDialsAsBefore(t *testing.T) {
	GuardConnections(nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	if resp, err := NewClient(time.Second).Do(context.Background(), Request{Method: MethodGet, URL: server.URL}); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Do = %d, %v", resp.StatusCode, err)
	}
}
