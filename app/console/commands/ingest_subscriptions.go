package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/ingestsubscription"
)

// ingestSubscriptionLister lists provider push subscriptions; *ingestsubscription.Service
// is the one implementation.
type ingestSubscriptionLister interface {
	List(ctx context.Context) ([]ingestsubscription.Listed, error)
}

// IngestSubscriptions lists the provider push subscriptions: the webhook host only,
// never a signing secret.
type IngestSubscriptions struct {
	subscriptions ingestSubscriptionLister
}

// NewIngestSubscriptions wires the command; the service is required.
func NewIngestSubscriptions(subscriptions ingestSubscriptionLister) *IngestSubscriptions {
	if subscriptions == nil {
		panic("ingest:subscriptions: subscription service is required")
	}
	return &IngestSubscriptions{subscriptions: subscriptions}
}

func (c *IngestSubscriptions) Signature() string {
	return "ingest:subscriptions"
}

func (c *IngestSubscriptions) Description() string {
	return "List the provider push subscriptions (provider, chain, status, last address sync, webhook host)"
}

func (c *IngestSubscriptions) Extend() command.Extend {
	return command.Extend{Category: "ingest"}
}

func (c *IngestSubscriptions) Handle(ctx console.Context) error {
	listed, err := c.subscriptions.List(context.Background())
	if err != nil {
		return fail(ctx, err)
	}
	if len(listed) == 0 {
		ctx.Info("no ingest subscriptions (create one with ingest:subscribe)")
		return nil
	}
	for _, sub := range listed {
		ctx.Line(fmt.Sprintf("%s %s: %s", sub.Provider, sub.ChainID, describeSubscription(sub)))
	}
	return nil
}

// describeSubscription is one subscription for an operator: no signing secret, the
// webhook by its host.
func describeSubscription(sub ingestsubscription.Listed) string {
	synced := "never"
	if sub.LastSyncedAt != nil {
		synced = sub.LastSyncedAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s, status %s, sync %s (last %s), host %s, provider webhook %s",
		sub.ID, sub.Status, sub.SyncStatus, synced, sub.Host, sub.ProviderWebhookID)
}
