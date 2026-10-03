package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/deposit"
)

const chainNetworkProfileConfigKey = "vault.chains.network_profile"

type ChainsAlignNetwork struct {
	deposits *deposit.Service
}

// NewChainsAlignNetwork points configured chains at a network profile.
func NewChainsAlignNetwork(deposits *deposit.Service) *ChainsAlignNetwork {
	return &ChainsAlignNetwork{deposits: deposits}
}

func (c *ChainsAlignNetwork) Signature() string {
	return "chains:align-network"
}

func (c *ChainsAlignNetwork) Description() string {
	return "Point eth/btc/polygon/sol at the networks of a profile (mainnet|testnet); dry run unless --apply"
}

func (c *ChainsAlignNetwork) Extend() command.Extend {
	return command.Extend{
		Category: "chains",
		Flags: []command.Flag{
			&command.StringFlag{Name: "profile", Usage: "mainnet or testnet (default: CHAIN_NETWORK_PROFILE)"},
			&command.StringFlag{Name: "account", Usage: "account UUID to move to the profile's environment"},
			&command.BoolFlag{Name: "apply", Usage: "write the changes (default: print them only)"},
		},
	}
}

func (c *ChainsAlignNetwork) Handle(ctx console.Context) error {
	profile := strings.TrimSpace(ctx.Option("profile"))
	if profile == "" {
		profile = strings.TrimSpace(facades.Config().GetString(chainNetworkProfileConfigKey))
	}
	if profile == "" {
		return failCommand(ctx, fmt.Errorf("no profile: pass --profile=mainnet|testnet or set CHAIN_NETWORK_PROFILE"))
	}

	var accountID uuid.UUID
	if raw := strings.TrimSpace(ctx.Option("account")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return failCommand(ctx, fmt.Errorf("invalid --account %q: %w", raw, err))
		}
		accountID = parsed
	}

	store := chainregistry.NewORMStore()
	background := context.Background()
	alignment, err := chainregistry.PlanAlignment(background, profile, store,
		decryptAndResolveRPCURL, chainregistry.ProbeRPCNetwork, accountID)
	if err != nil {
		return failCommand(ctx, err)
	}

	printAlignment(ctx, alignment)
	if alignment.IsEmpty() {
		return nil
	}
	if !ctx.OptionBool("apply") {
		ctx.Info("dry run: nothing written (pass --apply)")
		return nil
	}
	if err := chainregistry.ApplyAlignment(store, alignment); err != nil {
		return failCommand(ctx, err)
	}
	for chainID := range alignment.Reissues {
		refreshAddressCache(ctx, background, chainID, c.deposits)
	}
	ctx.Info("applied; restart the API and workers so the chain adapters reload")
	return nil
}

func printAlignment(ctx console.Context, alignment *chainregistry.Alignment) {
	plan := alignment.Plan
	for _, warning := range plan.Warnings {
		ctx.Warning(warning)
	}
	if alignment.IsEmpty() {
		ctx.Info(fmt.Sprintf("already aligned to the %s profile", plan.Profile))
		return
	}
	for _, change := range plan.Changes {
		ctx.Info(fmt.Sprintf("chain %s: network %q → %q, network_id %s → %s, is_testnet %t → %t",
			change.ChainID, change.FromNetwork, change.ToNetwork,
			formatNetworkID(change.FromNetworkID), formatNetworkID(change.ToNetworkID),
			change.FromTestnet, change.ToTestnet))
		for _, reissue := range alignment.Reissues[change.ChainID] {
			ctx.Info(fmt.Sprintf("  wallet %s (%s): retire %d address(es), genesis %s",
				reissue.WalletID, reissue.WalletLabel, len(reissue.Retired), formatGenesis(reissue.Genesis.Address)))
		}
	}
	if change := alignment.AccountChange; change != nil {
		ctx.Info(fmt.Sprintf("account %s: environment %s → %s", change.AccountID, change.From, change.To))
	}
}

// refreshAddressCache rebuilds the Redis set of watched addresses of the chain so
// retired addresses stop matching deposits.
func refreshAddressCache(ctx console.Context, background context.Context, chainID string, deposits *deposit.Service) {
	if deposits == nil {
		ctx.Warning(fmt.Sprintf("address cache of %s not refreshed: no deposit service", chainID))
		return
	}
	if err := deposits.RefreshAddressCache(background, chainID); err != nil {
		ctx.Warning(fmt.Sprintf("address cache of %s not refreshed: %s", chainID, err))
	}
}

func failCommand(ctx console.Context, err error) error {
	ctx.Error(err.Error())
	return err
}

func formatNetworkID(id *int64) string {
	if id == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *id)
}

func formatGenesis(address string) string {
	if address == "" {
		return "unchanged"
	}
	return address
}

func decryptAndResolveRPCURL(encrypted string) (string, error) {
	stored, err := facades.Crypt().DecryptString(encrypted)
	if err != nil {
		return "", err
	}
	return models.ResolveRPCURL(stored)
}
