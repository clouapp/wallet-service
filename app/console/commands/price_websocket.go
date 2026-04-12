package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/services/price"
)

type PriceWebSocket struct{}

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
	ctr := container.Get()

	ctx.Info("refreshing initial prices...")
	bgCtx := context.Background()
	if ctr.PriceService != nil {
		if err := ctr.PriceService.RefreshCryptoPrices(bgCtx); err != nil {
			ctx.Error("initial crypto refresh failed: " + err.Error())
		}
		if err := ctr.PriceService.RefreshFiatRates(bgCtx); err != nil {
			ctx.Error("initial fiat refresh failed: " + err.Error())
		}
	}
	ctx.Info("initial prices refreshed")

	apiKey := ctr.PriceConfig.CoinAPIKey
	if apiKey == "" {
		ctx.Error("COINAPI_API_KEY is not configured")
		return nil
	}

	ws := price.NewWebSocketClient(apiKey, ctr.CurrencyRepo, ctr.Redis)
	ctx.Info("starting CoinAPI WebSocket connection...")
	return ws.Connect(bgCtx)
}
