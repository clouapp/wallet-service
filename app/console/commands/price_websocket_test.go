package commands

import (
	"context"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/price"
)

type priceWebSocketCacheStub struct{}

func (priceWebSocketCacheStub) Get(context.Context, string) (string, error) {
	return "", nil
}

func (priceWebSocketCacheStub) Set(context.Context, string, []byte, time.Duration) error {
	return nil
}

func TestNewPriceWebSocketKeepsItsDependencies(t *testing.T) {
	prices := price.NewService(price.Deps{})
	cache := priceWebSocketCacheStub{}
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

func TestNewPriceWebSocketAllowsANilCache(t *testing.T) {
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
