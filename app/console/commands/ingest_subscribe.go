package commands

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/ingestsubscription"
)

// ingestSubscriber creates provider push subscriptions; *ingestsubscription.Service
// is the one implementation.
type ingestSubscriber interface {
	Subscribe(ctx context.Context, req ingestsubscription.Request) (ingestsubscription.Plan, error)
}

// IngestSubscribe creates the provider subscription (Alchemy, Helius, QuickNode) that
// pushes a chain's deposits to the ingest route; a dry run without --apply.
type IngestSubscribe struct {
	subscriptions ingestSubscriber
}

// NewIngestSubscribe wires the command; the service is required.
func NewIngestSubscribe(subscriptions ingestSubscriber) *IngestSubscribe {
	if subscriptions == nil {
		panic("ingest:subscribe: subscription service is required")
	}
	return &IngestSubscribe{subscriptions: subscriptions}
}

func (c *IngestSubscribe) Signature() string {
	return "ingest:subscribe"
}

func (c *IngestSubscribe) Description() string {
	return "Create the provider webhook that pushes a chain's deposits to the ingest base URL (the ingest_push.base_url platform setting, else WEBHOOK_INGEST_BASE_URL), store its subscription with the signing secret sealed and sync its addresses (dry run without --apply)"
}

func (c *IngestSubscribe) Extend() command.Extend {
	return command.Extend{
		Category: "ingest",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "provider", Usage: "alchemy (EVM), helius (Solana) or quicknode (Bitcoin)", Required: true},
			&command.ArgumentString{Name: "chain", Usage: "configured chain id, for example teth", Required: true},
		},
		Flags: []command.Flag{
			&command.BoolFlag{Name: "apply", Usage: "create the provider webhook and store the subscription (default: print the plan only)"},
		},
	}
}

func (c *IngestSubscribe) Handle(ctx console.Context) error {
	apply := ctx.OptionBool("apply")
	plan, err := c.subscriptions.Subscribe(context.Background(), ingestsubscription.Request{
		Provider: ctx.ArgumentString("provider"), ChainID: ctx.ArgumentString("chain"), Apply: apply,
	})
	printSubscribePlan(ctx, plan, apply)
	return fail(ctx, err)
}

func printSubscribePlan(ctx console.Context, plan ingestsubscription.Plan, apply bool) {
	if plan.BaseURLNote != "" {
		ctx.Warning(plan.BaseURLNote)
	}
	for _, missing := range plan.Missing {
		ctx.Warning("missing: " + missing)
	}
	if plan.Existing != nil {
		ctx.Info(fmt.Sprintf("the %s subscription of %s exists: %s; nothing to do", plan.Provider, plan.ChainID, describeSubscription(*plan.Existing)))
		return
	}
	if plan.WebhookURL == "" || len(plan.Missing) > 0 {
		return
	}
	ctx.Info(fmt.Sprintf("%s subscription of %s: network %s (%s), webhook %s, %d watched addresses",
		plan.Provider, plan.ChainID, plan.Network, plan.ProviderNetwork, plan.WebhookURL, plan.Addresses))
	ctx.Info("base URL read from " + plan.BaseURLSource)
	switch {
	case plan.Applied:
		ctx.Info(fmt.Sprintf("created subscription %s, provider webhook %s, signing secret stored sealed", plan.SubscriptionID, plan.ProviderWebhookID))
	case !apply:
		ctx.Info("dry run: nothing created (pass --apply)")
	}
}
