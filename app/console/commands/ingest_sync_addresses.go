package commands

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
)

// chainAddressSyncer pushes a chain's watched accounts to its provider webhook;
// *webhooksync.Service is the one implementation.
type chainAddressSyncer interface {
	SyncChainAddresses(ctx context.Context, chainID string) error
}

// IngestSyncAddresses pushes, now, the accounts a chain's provider subscription
// watches (the chain's active addresses, expanded by its adapter: on Solana each
// address and its token accounts). It otherwise runs when an address is created or
// a subscription is: this command applies a change in what is watched (a token
// registered, an expansion added) without waiting for the next address.
type IngestSyncAddresses struct {
	sync chainAddressSyncer
}

// NewIngestSyncAddresses wires the command; the sync service is required.
func NewIngestSyncAddresses(sync chainAddressSyncer) *IngestSyncAddresses {
	if sync == nil {
		panic("ingest:sync-addresses: webhook sync service is required")
	}
	return &IngestSyncAddresses{sync: sync}
}

func (c *IngestSyncAddresses) Signature() string {
	return "ingest:sync-addresses"
}

func (c *IngestSyncAddresses) Description() string {
	return "Push the accounts a chain's provider subscription watches to its webhook now"
}

func (c *IngestSyncAddresses) Extend() command.Extend {
	return command.Extend{
		Category: "ingest",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain", Usage: "configured chain id with an active subscription, for example sol", Required: true},
		},
	}
}

func (c *IngestSyncAddresses) Handle(ctx console.Context) error {
	chainID := ctx.ArgumentString("chain")
	if err := c.sync.SyncChainAddresses(context.Background(), chainID); err != nil {
		return fail(ctx, err)
	}
	ctx.Info(fmt.Sprintf("the watched accounts of %s were pushed to its provider (ingest:subscriptions shows the sync)", chainID))
	return nil
}
