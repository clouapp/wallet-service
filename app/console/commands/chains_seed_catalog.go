package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
)

// CatalogSeeder writes the reference catalog of a new environment: the primary chain
// records (eth, btc, polygon, sol) on the network CHAIN_NETWORK_PROFILE names, their
// sweep thresholds, tokens and explorer resources, and the currencies. It is what
// DatabaseSeeder runs before the dev-only seeders (users, accounts, wallets), which
// never run on a shared host.
type CatalogSeeder func(ctx context.Context) error

// ChainsSeedCatalog is the first-boot step of a fresh database: `migrate`, then
// `chains:seed-catalog --apply`, then `chains:add-missing --apply`.
type ChainsSeedCatalog struct {
	seed CatalogSeeder
}

// NewChainsSeedCatalog wires the catalog seeder.
func NewChainsSeedCatalog(seed CatalogSeeder) *ChainsSeedCatalog {
	if seed == nil {
		panic("chains:seed-catalog: seeder is required")
	}
	return &ChainsSeedCatalog{seed: seed}
}

func (c *ChainsSeedCatalog) Signature() string { return "chains:seed-catalog" }

func (c *ChainsSeedCatalog) Description() string {
	return "Seed the reference catalog of a fresh database (primary chains on the configured network profile, thresholds, tokens, explorers, currencies); dry run unless --apply"
}

func (c *ChainsSeedCatalog) Extend() command.Extend {
	return command.Extend{
		Category: "chains",
		Flags: []command.Flag{
			&command.BoolFlag{Name: "apply", Usage: "write the catalog (default: print the steps only)"},
		},
	}
}

// catalogSeedSteps is what the seeder writes, in order.
var catalogSeedSteps = []string{
	"chains: eth, btc, polygon, sol (and their t-prefixed records) on the network CHAIN_NETWORK_PROFILE names",
	"sweep thresholds of those chains",
	"tokens of those networks",
	"explorer and faucet resources of those networks",
	"currencies (crypto and fiat)",
}

const catalogSeedWarning = "an existing chain keeps its row, but its rpc_url is re-sealed from the environment variable the catalog names: run this once, on a fresh database, before chains:rpc-endpoints"

func (c *ChainsSeedCatalog) Handle(ctx console.Context) error {
	printReport(ctx, catalogSeedSteps, []string{catalogSeedWarning}, nil, nil)
	if !ctx.OptionBool("apply") {
		ctx.Info("dry run: nothing written (pass --apply)")
		return nil
	}
	if err := c.seed(context.Background()); err != nil {
		return fail(ctx, err)
	}
	ctx.Info("catalog seeded; next: chains:add-missing --apply, then chains:rpc-endpoints")
	return nil
}
