package price

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
)

func TestNew_Web_SocketClientKeepsItsDependencies(t *testing.T) {
	repo := &mockCurrencyRepo{}
	dialer := &recordingDialer{}
	client := NewWebSocketClient(WebSocketClientDeps{
		APIKey:     "key",
		Currencies: repo,
		Dialer:     dialer,
	})
	if client == nil || client.apiKey != "key" || client.currencyRepo != repo || client.cache != nil || client.dialer != dialer {
		t.Fatal("websocket client did not keep its dependencies")
	}

	bare := NewWebSocketClient(WebSocketClientDeps{})
	if bare == nil || bare.apiKey != "" || bare.currencyRepo != nil || bare.cache != nil || bare.dialer != nil {
		t.Fatal("missing dependencies were not left unset")
	}
}

func TestConnect_Refuses_AMissingDialer(t *testing.T) {
	client := NewWebSocketClient(WebSocketClientDeps{
		APIKey:     "key",
		Currencies: &mockCurrencyRepo{},
	})
	err := client.Connect(context.Background())
	if err == nil || err.Error() != "coinapi quote dialer is not configured" {
		t.Fatalf("error = %v", err)
	}
}

func TestConnect_Dials_CoinAPIAndSendsHello(t *testing.T) {
	repo := &mockCurrencyRepo{active: []models.Currency{{Code: "BTC"}}}
	conn := &recordingConn{
		hello:  make(chan map[string]any, 1),
		closed: make(chan struct{}),
	}
	dialer := &recordingDialer{conn: conn}
	client := NewWebSocketClient(WebSocketClientDeps{
		APIKey:     "test-key",
		Currencies: repo,
		Dialer:     dialer,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Connect(ctx) }()

	select {
	case hello := <-conn.hello:
		if dialer.url != wsURL {
			t.Fatalf("dial url = %s", dialer.url)
		}
		want := map[string]any{
			"type":                             "hello",
			"apikey":                           "test-key",
			"heartbeat":                        false,
			"subscribe_data_type":              []string{"exrate"},
			"subscribe_filter_asset_id":        []string{"BTC/USD"},
			"subscribe_filter_exchange_id":     []string{wsExchange},
			"subscribe_update_limit_ms_exrate": wsUpdateInterval * 1000,
		}
		if !reflect.DeepEqual(hello, want) {
			t.Fatalf("hello = %#v", hello)
		}
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the hello")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("connect error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connect did not return after cancel")
	}
}

type recordingDialer struct {
	conn *recordingConn
	url  string
}

func (d *recordingDialer) Dial(_ context.Context, url string) (QuoteConn, error) {
	d.url = url
	return d.conn, nil
}

type recordingConn struct {
	hello  chan map[string]any
	closed chan struct{}
}

func (c *recordingConn) WriteJSON(v any) error {
	msg, ok := v.(map[string]any)
	if !ok {
		return errStale
	}
	c.hello <- msg
	return nil
}

func (c *recordingConn) ReadMessage() (int, []byte, error) {
	<-c.closed
	return 0, nil, errStale
}

func (c *recordingConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}
