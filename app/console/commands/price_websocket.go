package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/price"
	"github.com/redis/go-redis/v9"
)

type PriceWebSocket struct {
	prices     *price.Service
	coinAPIKey string
	redis      *redis.Client
}

// NewPriceWebSocket streams CoinAPI prices. redis may be nil when Redis is not configured.
func NewPriceWebSocket(prices *price.Service, coinAPIKey string, redisClient *redis.Client) *PriceWebSocket {
	return &PriceWebSocket{prices: prices, coinAPIKey: coinAPIKey, redis: redisClient}
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

	ws := c.prices.PriceWebSocket(apiKey, c.redis)
	ctx.Info("starting CoinAPI WebSocket connection...")
	return ws.Connect(bgCtx)
}
