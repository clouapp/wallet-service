package commands

import (
	"testing"

	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/tests/memcache"
)

func TestNew_Price_WebSocketKeepsItsDependencies(t *testing.T) {
	prices := price.NewService(price.Deps{})
	cache := memcache.New()
	cmd := NewPriceWebSocket(PriceWebSocketDeps{
		Prices:     prices,
		CoinAPIKey: "test-key",
		Cache:      cache,
	})
	if cmd == nil {
		t.Fatal("NewPriceWebSocket returned nil")
	}
	if cmd.prices != prices {
		t.Fatal("price websocket did not keep the price service")
	}
	if cmd.coinAPIKey != "test-key" {
		t.Fatal("price websocket did not keep the CoinAPI key")
	}
	if cmd.cache != cache {
		t.Fatal("price websocket did not keep the price cache")
	}
}

func TestNew_Price_WebSocketAllowsANilCache(t *testing.T) {
	prices := price.NewService(price.Deps{})
	cmd := NewPriceWebSocket(PriceWebSocketDeps{Prices: prices, CoinAPIKey: "test-key"})
	if cmd == nil {
		t.Fatal("NewPriceWebSocket returned nil")
	}
	if cmd.prices != prices {
		t.Fatal("price websocket did not keep the price service")
	}
	if cmd.coinAPIKey != "test-key" {
		t.Fatal("price websocket did not keep the CoinAPI key")
	}
	if cmd.cache != nil {
		t.Fatal("cache should stay nil when omitted")
	}
}
