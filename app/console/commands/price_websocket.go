package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/price"
)

type PriceWebSocket struct {
	prices     *price.Service
	coinAPIKey string
	cache      price.PriceCache
}

// PriceWebSocketDeps is everything the price:websocket command needs. Cache may
// be nil when Redis is not configured.
type PriceWebSocketDeps struct {
	Prices     *price.Service
	CoinAPIKey string
	Cache      price.PriceCache
}

// NewPriceWebSocket streams CoinAPI prices.
func NewPriceWebSocket(deps PriceWebSocketDeps) *PriceWebSocket {
	return &PriceWebSocket{prices: deps.Prices, coinAPIKey: deps.CoinAPIKey, cache: deps.Cache}
}

func (c *PriceWebSocket) Signature() string {
	return "price:websocket"
}

func (c *PriceWebSocket) Description() string {
	return "Connect to CoinAPI WebSocket for real-time crypto price updates"
}

func (c *PriceWebSocket) Extend() command.Extend {
	return command.Extend{Category: "price"}
}

func (c *PriceWebSocket) Handle(ctx console.Context) error {
	ctx.Info("refreshing initial prices...")
	bgCtx := context.Background()
	if c.prices != nil {
		if err := c.prices.RefreshCryptoPrices(bgCtx); err != nil {
			ctx.Error("initial crypto refresh failed: " + err.Error())
		}
		if err := c.prices.RefreshFiatRates(bgCtx); err != nil {
			ctx.Error("initial fiat refresh failed: " + err.Error())
		}
	}
	ctx.Info("initial prices refreshed")

	apiKey := c.coinAPIKey
	if apiKey == "" {
		ctx.Error("COINAPI_API_KEY is not configured")
		return nil
	}

	ws := c.prices.PriceWebSocket(apiKey, c.cache)
	ctx.Info("starting CoinAPI WebSocket connection...")
	return ws.Connect(bgCtx)
}
