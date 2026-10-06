package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/chainregistry"
)

// ChainsAddMissing creates the base/arbitrum/bsc records (and their t-prefixed
// test records) that a live registry lacks, without touching any existing row.
type ChainsAddMissing struct {
	missing *chainregistry.MissingChains
}

// NewChainsAddMissing wires the missing-chain service.
func NewChainsAddMissing(missing *chainregistry.MissingChains) *ChainsAddMissing {
	if missing == nil {
		panic("chains:add-missing: seeder is required")
	}
	return &ChainsAddMissing{missing: missing}
}

func (c *ChainsAddMissing) Signature() string { return "chains:add-missing" }

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
	report, err := c.missing.Add(context.Background(), ctx.OptionBool("apply"))
	printReport(ctx, report.Info, report.Warning, nil, nil)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
