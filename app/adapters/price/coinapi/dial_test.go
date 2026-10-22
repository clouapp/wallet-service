package coinapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/tests/fakedns"
)

// testDialer dials through the RPC guard, as the provider wires it: the test
// servers listen on the loopback, which the RPC policy allows.
var testDialer = NewDialer(egress.NewGuard(egress.RPCPolicy(false), nil).DialContext)

func TestDial_Rejects_MissingContextAndURL(t *testing.T) {
	var noContext context.Context // a nil context is the case under test
	if _, err := testDialer.Dial(noContext, "ws://example.test"); err == nil {
		t.Fatal("expected error for a nil context")
	}
	if _, err := testDialer.Dial(context.Background(), "  "); err == nil {
		t.Fatal("expected error for a blank url")
	}
}

func TestDial_Round_Trip(t *testing.T) {
	received := make(chan map[string]string, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var msg map[string]string
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		received <- msg
		_ = conn.WriteJSON(map[string]string{"ok": "1"})
	}))
	t.Cleanup(server.Close)

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, err := testDialer.Dial(context.Background(), socketURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.WriteJSON(map[string]string{"type": "hello"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case got := <-received:
		if got["type"] != "hello" {
			t.Fatalf("server received %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive the hello")
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(payload)) != `{"ok":"1"}` {
		t.Fatalf("payload = %s", payload)
	}
}

func TestDial_Canceled_Context(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := testDialer.Dial(ctx, "ws://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected dial to fail on a canceled context")
	}
}

func TestDial_RefusesAQuoteHostThatResolvesToAMetadataAddress(t *testing.T) {
	var reached atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Add(1) }))
	t.Cleanup(server.Close)
	dns := fakedns.Start(t, map[string][]string{"ws.coinapi.example.test": {"169.254.169.254"}})
	dialer := NewDialer(egress.NewGuard(egress.RPCPolicy(false), dns.Resolver()).DialContext)

	const rawURL = "wss://ws.coinapi.example.test/v1/?apikey=quote-secret"
	_, err := dialer.Dial(context.Background(), rawURL)
	if !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("err = %v, want the guard's refusal", err)
	}
	if strings.Contains(err.Error(), "quote-secret") {
		t.Fatalf("the refusal %q carries the key", err)
	}
	if _, err := dialer.Dial(context.Background(), "ws://169.254.169.254/v1/"); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("a metadata literal: err = %v", err)
	}
	if reached.Load() != 0 {
		t.Fatal("a refused destination was reached")
	}
}

func TestDial_WithoutAConnectionGuardDialsNothing(t *testing.T) {
	if _, err := (Dialer{}).Dial(context.Background(), "ws://127.0.0.1:1/v1/"); err == nil || !strings.Contains(err.Error(), "guard") {
		t.Fatalf("err = %v, want a refusal for the missing guard", err)
	}
}

func TestNew_Dialer_RequiresADial(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a dialer without a dial was built")
		}
	}()
	NewDialer(nil)
}
