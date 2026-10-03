package price

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/shopspring/decimal"
	"log/slog"
	"strings"
	"time"
)

// QuoteConn is one live CoinAPI quote socket.
type QuoteConn interface {
	WriteJSON(v any) error
	ReadMessage() (messageType int, payload []byte, err error)
	Close() error
}

// QuoteDialer opens a CoinAPI quote socket. The service keeps the URL and the reconnect loop.
type QuoteDialer interface {
	Dial(ctx context.Context, url string) (QuoteConn, error)
}

const (
	wsURL            = "wss://api-ncsa.coinapi.io/v1/"
	wsUpdateInterval = 10
	wsExchange       = "BINANCE"
)

var wsAssetMapping = map[string]string{}

var wsReverseMapping map[string]string

func init() {
	wsReverseMapping = make(map[string]string, len(wsAssetMapping))
	for code, asset := range wsAssetMapping {
		wsReverseMapping[asset] = code
	}
}

type WebSocketClient struct {
	apiKey       string
	currencyRepo currencyStore
	cache        PriceCache
	dialer       QuoteDialer
	activeCodes  []string
}

func NewWebSocketClient(apiKey string, currencyRepo currencyStore, cache PriceCache, dialer QuoteDialer) *WebSocketClient {
	return &WebSocketClient{
		apiKey:       apiKey,
		currencyRepo: currencyRepo,
		cache:        cache,
		dialer:       dialer,
	}
}

func (w *WebSocketClient) Connect(ctx context.Context) error {
	if w == nil {
		return fmt.Errorf("coinapi websocket client is nil")
	}
	if w.dialer == nil {
		return fmt.Errorf("coinapi quote dialer is not configured")
	}
	if err := w.refreshActiveCodes(ctx); err != nil {
		return fmt.Errorf("load active codes: %w", err)
	}
	if len(w.activeCodes) == 0 {
		return fmt.Errorf("no active crypto currencies to track")
	}

	backoff := time.Second
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		slog.Info("connecting to CoinAPI WebSocket", "url", wsURL)
		conn, err := w.dialer.Dial(ctx, wsURL)
		if err != nil {
			slog.Error("websocket dial failed", "error", err)
			time.Sleep(backoff)
			backoff = minDuration(backoff*2, maxBackoff)
			continue
		}

		backoff = time.Second

		hello := w.buildHelloMessage()
		if err := conn.WriteJSON(hello); err != nil {
			slog.Error("websocket send hello failed", "error", err)
			conn.Close()
			continue
		}
		slog.Info("CoinAPI WebSocket connected", "codes", len(w.activeCodes))

		refreshTicker := time.NewTicker(60 * time.Second)
		done := make(chan struct{})

		go func() {
			defer close(done)
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					slog.Error("websocket read failed", "error", err)
					return
				}
				w.processMessage(ctx, message)
			}
		}()

		select {
		case <-done:
			refreshTicker.Stop()
			conn.Close()
			slog.Warn("websocket disconnected, reconnecting...")
		case <-ctx.Done():
			refreshTicker.Stop()
			conn.Close()
			return ctx.Err()
		case <-refreshTicker.C:
			_ = w.refreshActiveCodes(ctx)
		}
	}
}

func (w *WebSocketClient) buildHelloMessage() map[string]interface{} {
	assets := make([]string, len(w.activeCodes))
	for i, code := range w.activeCodes {
		asset := code
		if mapped, ok := wsAssetMapping[code]; ok {
			asset = mapped
		}
		assets[i] = asset + "/USD"
	}

	return map[string]interface{}{
		"type":                             "hello",
		"apikey":                           w.apiKey,
		"heartbeat":                        false,
		"subscribe_data_type":              []string{"exrate"},
		"subscribe_filter_asset_id":        assets,
		"subscribe_filter_exchange_id":     []string{wsExchange},
		"subscribe_update_limit_ms_exrate": wsUpdateInterval * 1000,
	}
}

func (w *WebSocketClient) processMessage(ctx context.Context, data []byte) {
	var msg struct {
		AssetIDBase string          `json:"asset_id_base"`
		Rate        decimal.Decimal `json:"rate"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	rate, ok := fitQuotedPrice(msg.AssetIDBase, msg.Rate)
	if !ok {
		return
	}

	code := msg.AssetIDBase
	if reversed, ok := wsReverseMapping[code]; ok {
		code = reversed
	}

	found := false
	for _, c := range w.activeCodes {
		if strings.EqualFold(c, code) {
			found = true
			break
		}
	}
	if !found {
		return
	}

	cur, err := w.currencyRepo.FindByCode(ctx, code)
	if err != nil || cur == nil {
		return
	}

	oldPrice := cur.CurrentPrice.Decimal
	if err := w.currencyRepo.SetPrice(ctx, code, rate, oldPrice); err != nil {
		slog.Warn("ws update price failed", "code", code, "error", err)
		return
	}

	if w.cache != nil {
		if err := w.cache.Set(ctx, "currency:"+code, []byte(rate.String()), redisCurrencyTTL); err != nil {
			slog.Warn("ws redis cache currency failed", "code", code, "error", err)
		}
	}

	slog.Info("ws price updated", "code", code, "price", rate.String())
}

func (w *WebSocketClient) refreshActiveCodes(ctx context.Context) error {
	cryptos, err := w.currencyRepo.FindActiveCryptos(ctx)
	if err != nil {
		return err
	}
	codes := make([]string, len(cryptos))
	for i, c := range cryptos {
		codes[i] = c.Code
	}
	w.activeCodes = codes
	return nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
