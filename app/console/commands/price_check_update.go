package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/price"
)

type PriceCheckUpdate struct {
	prices *price.Service
}

// NewPriceCheckUpdate refreshes stale currency prices.
func NewPriceCheckUpdate(prices *price.Service) *PriceCheckUpdate {
	return &PriceCheckUpdate{prices: prices}
}

func (c *PriceCheckUpdate) Signature() string { return "price:check-update" }

func (c *PriceCheckUpdate) Description() string {
	return "Check for stale currency prices and trigger REST refresh"
}

func (c *PriceCheckUpdate) Extend() command.Extend {
	return command.Extend{Category: "price"}
}

func (c *PriceCheckUpdate) Handle(ctx console.Context) error {
	out, err := c.prices.CheckUpdate(context.Background())
	printReport(ctx, out.Info, nil, nil, nil)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
