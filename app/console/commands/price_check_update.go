package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
)

type PriceCheckUpdate struct{}

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
	ctr := container.Get()
	bgCtx := context.Background()

	staleCryptos, err := ctr.CurrencyRepo.FindStale(models.CurrencyTypeCrypto, 1*time.Minute)
	if err != nil {
		ctx.Error("failed to check stale cryptos: " + err.Error())
		return err
	}

	if len(staleCryptos) > 0 {
		ctx.Info(fmt.Sprintf("found %d stale crypto currencies, refreshing...", len(staleCryptos)))
		if ctr.PriceService != nil {
			if err := ctr.PriceService.RefreshCryptoPrices(bgCtx); err != nil {
				ctx.Error("crypto refresh failed: " + err.Error())
			}
		}

		stillStale, _ := ctr.CurrencyRepo.FindStale(models.CurrencyTypeCrypto, 1*time.Minute)
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

	staleFiats, err := ctr.CurrencyRepo.FindStale(models.CurrencyTypeFiat, 1*time.Hour)
	if err != nil {
		ctx.Error("failed to check stale fiats: " + err.Error())
		return err
	}

	if len(staleFiats) > 0 {
		ctx.Info(fmt.Sprintf("found %d stale fiat currencies, refreshing...", len(staleFiats)))
		if ctr.PriceService != nil {
			if err := ctr.PriceService.RefreshFiatRates(bgCtx); err != nil {
				ctx.Error("fiat refresh failed: " + err.Error())
			}
		}
	} else {
		ctx.Info("all fiat rates are up to date")
	}

	return nil
}
