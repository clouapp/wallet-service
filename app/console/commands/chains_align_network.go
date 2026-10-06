package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/chainregistry"
)

type ChainsAlignNetwork struct {
	aligner *chainregistry.Aligner
}

// NewChainsAlignNetwork points configured chains at a network profile.
func NewChainsAlignNetwork(aligner *chainregistry.Aligner) *ChainsAlignNetwork {
	return &ChainsAlignNetwork{aligner: aligner}
}

func (c *ChainsAlignNetwork) Signature() string { return "chains:align-network" }

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
	report, err := c.aligner.Align(context.Background(), ctx.Option("profile"), ctx.Option("account"), ctx.OptionBool("apply"))
	printReport(ctx, report.Info, report.Warning, nil, nil)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
