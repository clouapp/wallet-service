package commands

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/macrowallets/waas/app/services/ramps"
)

type RampsPruneTimeline struct{ service *ramps.Service }

func NewRampsPruneTimeline(service *ramps.Service) *RampsPruneTimeline {
	return &RampsPruneTimeline{service}
}
func (*RampsPruneTimeline) Signature() string { return "ramps:prune-timeline" }
func (*RampsPruneTimeline) Description() string {
	return "Prune provider timeline metadata after its retention period"
}
func (*RampsPruneTimeline) Extend() command.Extend {
	return command.Extend{Category: "ramps", Flags: []command.Flag{&command.IntFlag{Name: "days", Value: ramps.ProviderTimelineRetentionDays}}}
}
func (c *RampsPruneTimeline) Handle(ctx console.Context) error {
	count, err := c.service.PruneTimeline(context.Background(), ctx.OptionInt("days"))
	if err != nil {
		return fail(ctx, err)
	}
	ctx.Info(fmt.Sprintf("Provider timeline entries pruned: %d", count))
	return nil
}

type RampsBlindPayCheck struct{ service *ramps.Service }

func NewRampsBlindPayCheck(service *ramps.Service) *RampsBlindPayCheck {
	return &RampsBlindPayCheck{service}
}
func (*RampsBlindPayCheck) Signature() string { return "ramp:blindpay-check" }
func (*RampsBlindPayCheck) Description() string {
	return "Check the configured BlindPay instance without creating an operation"
}
func (*RampsBlindPayCheck) Extend() command.Extend {
	return command.Extend{Category: "ramps", Flags: []command.Flag{&command.StringFlag{Name: "environment", Value: "sandbox"}}}
}
func (c *RampsBlindPayCheck) Handle(ctx console.Context) error {
	instance, err := c.service.CheckBlindPay(context.Background(), ctx.Option("environment"))
	if err != nil {
		return fail(ctx, err)
	}
	ctx.Info(fmt.Sprintf("BlindPay instance reachable: %s", instance))
	return nil
}
