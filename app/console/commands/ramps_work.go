package commands

import (
	"context"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/macrowallets/waas/app/services/ramps"
	"github.com/macrowallets/waas/app/services/webhook"
)

type RampsWork struct {
	ramps    *ramps.Service
	webhooks *webhook.Service
}

func NewRampsWork(r *ramps.Service, w *webhook.Service) *RampsWork { return &RampsWork{r, w} }
func (*RampsWork) Signature() string                               { return "ramps:work" }
func (*RampsWork) Description() string {
	return "Process fiat verification, settlement, verified webhooks and recovery"
}
func (*RampsWork) Extend() command.Extend { return command.Extend{Category: "ramps"} }
func (c *RampsWork) Handle(ctx console.Context) error {
	if err := c.ramps.Tick(context.Background(), c.webhooks, true); err != nil {
		return fail(ctx, err)
	}
	return nil
}
