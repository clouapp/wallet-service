package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/database/seeds"
)

// ChainsAddMissing creates the base/arbitrum/bsc records (and their t-prefixed
// test records) that a live registry lacks, without touching any existing row.
type ChainsAddMissing struct{}

func (c *ChainsAddMissing) Signature() string {
	return "chains:add-missing"
}

func (c *ChainsAddMissing) Description() string {
	return "Create missing base/arbitrum/bsc chain records with tokens, explorers and thresholds; dry run unless --apply"
}

func (c *ChainsAddMissing) Extend() command.Extend {
	return command.Extend{
		Category: "chains",
		Flags: []command.Flag{
			&command.BoolFlag{Name: "apply", Usage: "write the records (default: print them only)"},
		},
	}
}

func (c *ChainsAddMissing) Handle(ctx console.Context) error {
	background := context.Background()
	plan, err := seeds.SeedMissingAddedChains(background, false)
	if err != nil {
		return failCommand(ctx, err)
	}
	for _, id := range plan.Skipped {
		ctx.Info(fmt.Sprintf("%s: already in the registry, left as is", id))
	}
	if len(plan.Chains) == 0 {
		ctx.Info("nothing to add")
		return nil
	}
	for _, added := range plan.Chains {
		if err := checkAddedChainRPC(background, added); err != nil {
			return failCommand(ctx, err)
		}
		ctx.Info(fmt.Sprintf("%s: create on %s (network_id %d, is_testnet %t), rpc_url env:%s",
			added.ID, added.Network, added.NetworkID, added.IsTestnet, added.EnvVar))
	}
	for _, token := range plan.Tokens {
		ctx.Info("token " + token)
	}
	for _, resource := range plan.Resources {
		ctx.Info("resource " + resource)
	}
	if !ctx.OptionBool("apply") {
		ctx.Info("dry run: nothing written (pass --apply)")
		return nil
	}
	if _, err := seeds.SeedMissingAddedChains(background, true); err != nil {
		return failCommand(ctx, err)
	}
	ctx.Info(fmt.Sprintf("created %d chains; restart the API so it registers their adapters", len(plan.Chains)))
	return nil
}

// checkAddedChainRPC refuses a record whose environment RPC is unset or serves
// another network, so no adapter signs for a network its row does not name.
func checkAddedChainRPC(ctx context.Context, added seeds.AddedChain) error {
	rpcURL, err := models.ResolveRPCURL(models.RPCURLEnvPrefix + added.EnvVar)
	if err != nil {
		return fmt.Errorf("%s: %w", added.ID, err)
	}
	record := models.Chain{ID: added.ID, AdapterType: models.AdapterTypeEVM}
	served, err := chainregistry.ProbeRPCNetwork(ctx, record, rpcURL)
	if err != nil {
		return fmt.Errorf("%s: probe %s: %w", added.ID, added.EnvVar, err)
	}
	if !strings.EqualFold(served, added.Network) {
		return fmt.Errorf("%w: %s serves %q, %s needs %q", chainregistry.ErrRPCNetworkMismatch, added.EnvVar, served, added.ID, added.Network)
	}
	return nil
}
