package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/price"
)

type PriceCheckUpdate struct {
	prices *price.Service
}

// NewPriceCheckUpdate refreshes stale currency prices.
func NewPriceCheckUpdate(prices *price.Service) *PriceCheckUpdate {
	return &PriceCheckUpdate{prices: prices}
}

func (c *PriceCheckUpdate) Signature() string {
	return "price:check-update"
}

func (c *PriceCheckUpdate) Description() string {
	return "Check for stale currency prices and trigger REST refresh"
}

func (c *PriceCheckUpdate) Extend() command.Extend {
	return command.Extend{Category: "price"}
}

func (c *PriceCheckUpdate) Handle(ctx console.Context) error {
	bgCtx := context.Background()

	staleCryptos, err := c.prices.FindStale(bgCtx, models.CurrencyTypeCrypto, 1*time.Minute)
	if err != nil {
		ctx.Error("failed to check stale cryptos: " + err.Error())
		return err
	}

	if len(staleCryptos) > 0 {
		ctx.Info(fmt.Sprintf("found %d stale crypto currencies, refreshing...", len(staleCryptos)))
		if c.prices != nil {
			if err := c.prices.RefreshCryptoPrices(bgCtx); err != nil {
				ctx.Error(redactedLine("crypto refresh failed: " + err.Error()))
			}
		}

		stillStale, _ := c.prices.FindStale(bgCtx, models.CurrencyTypeCrypto, 1*time.Minute)
		if len(stillStale) > 0 {
			codes := make([]string, len(stillStale))
			for i, c := range stillStale {
				codes[i] = c.Code
			}
			ctx.Error(fmt.Sprintf("still stale after all providers: %v", codes))
		} else {
			ctx.Info("all crypto prices refreshed successfully")
		}
	} else {
		ctx.Info("all crypto prices are up to date")
	}

	staleFiats, err := c.prices.FindStale(bgCtx, models.CurrencyTypeFiat, 1*time.Hour)
	if err != nil {
		ctx.Error("failed to check stale fiats: " + err.Error())
		return err
	}

	if len(staleFiats) > 0 {
		ctx.Info(fmt.Sprintf("found %d stale fiat currencies, refreshing...", len(staleFiats)))
		if c.prices != nil {
			if err := c.prices.RefreshFiatRates(bgCtx); err != nil {
				ctx.Error(redactedLine("fiat refresh failed: " + err.Error()))
			}
		}
	} else {
		ctx.Info("all fiat rates are up to date")
	}

	return nil
}
