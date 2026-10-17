package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/chainregistry"
)

// ChainsAlignTokens moves the tokens of the primary records that point at a test
// network (CHAIN_NETWORK_PROFILE=testnet) onto that network's contracts. A record on a
// mainnet network is never touched.
type ChainsAlignTokens struct {
	tokens *chainregistry.NetworkTokens
}

// NewChainsAlignTokens wires the token alignment service.
func NewChainsAlignTokens(tokens *chainregistry.NetworkTokens) *ChainsAlignTokens {
	return &ChainsAlignTokens{tokens: tokens}
}

func (c *ChainsAlignTokens) Signature() string { return "chains:align-tokens" }

func (c *ChainsAlignTokens) Description() string {
	return "Point the tokens of eth/polygon/sol/... records on a test network at that network's contracts, deactivating the mainnet ones; dry run unless --apply"
}

func (c *ChainsAlignTokens) Extend() command.Extend {
	return command.Extend{
		Category: "chains",
		Flags: []command.Flag{
			&command.BoolFlag{Name: "apply", Usage: "write the changes (default: print them only)"},
		},
	}
}

func (c *ChainsAlignTokens) Handle(ctx console.Context) error {
	report, err := c.tokens.Align(context.Background(), ctx.OptionBool("apply"))
	printReport(ctx, report.Info, report.Warning, nil, nil)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
