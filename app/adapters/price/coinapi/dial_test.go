package coinapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDialRejectsMissingContextAndURL(t *testing.T) {
	if _, err := (Dialer{}).Dial(nil, "ws://example.test"); err == nil {
		t.Fatal("expected error for a nil context")
	}
	if _, err := (Dialer{}).Dial(context.Background(), "  "); err == nil {
		t.Fatal("expected error for a blank url")
	}
}

func TestDialRoundTrip(t *testing.T) {
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
	conn, err := (Dialer{}).Dial(context.Background(), socketURL)
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

func TestDialCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Dialer{}).Dial(ctx, "ws://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected dial to fail on a canceled context")
	}
}
