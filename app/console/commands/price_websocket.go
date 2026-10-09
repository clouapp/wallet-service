package commands

import (
	"context"

	contractscache "github.com/goravel/framework/contracts/cache"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/price"
)

type PriceWebSocket struct {
	prices     *price.Service
	coinAPIKey string
	cache      contractscache.Driver
}

// PriceWebSocketDeps is everything the price:websocket command needs. Cache may
// be nil when Redis is not configured.
type PriceWebSocketDeps struct {
	Prices     *price.Service
	CoinAPIKey string
	Cache      contractscache.Driver
}

// NewPriceWebSocket streams CoinAPI prices.
func NewPriceWebSocket(deps PriceWebSocketDeps) *PriceWebSocket {
	return &PriceWebSocket{prices: deps.Prices, coinAPIKey: deps.CoinAPIKey, cache: deps.Cache}
}

func (c *PriceWebSocket) Signature() string { return "price:websocket" }

func (c *PriceWebSocket) Description() string {
	return "Connect to CoinAPI WebSocket for real-time crypto price updates"
}

func (c *PriceWebSocket) Extend() command.Extend {
	return command.Extend{Category: "price"}
}

func (c *PriceWebSocket) Handle(ctx console.Context) error {
	err := c.prices.Stream(context.Background(), c.coinAPIKey, c.cache, func(level, message string) {
		if level == "error" {
			ctx.Error(message)
			return
		}
		ctx.Info(message)
	})
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
