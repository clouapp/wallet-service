package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/refresh"
)

type RefreshAddress struct {
	balances *refresh.BalanceService
	run      *refresh.Operator
}

// RefreshAddressDeps is everything the refresh:address command needs.
// Balances is required.
type RefreshAddressDeps struct {
	Balances  *refresh.BalanceService
	Wallets   refresh.WalletLookup
	Addresses refresh.AddressLookup
}

// NewRefreshAddress refreshes the wallets that own the given addresses.
func NewRefreshAddress(deps RefreshAddressDeps) *RefreshAddress {
	if deps.Balances == nil {
		panic("refresh:address: balance refresh service is required")
	}
	return &RefreshAddress{
		balances: deps.Balances,
		run: refresh.NewOperator(refresh.OperatorDeps{
			Balances:  deps.Balances,
			Wallets:   deps.Wallets,
			Addresses: deps.Addresses,
		}),
	}
}

func (c *RefreshAddress) Signature() string { return "refresh:address" }

func (c *RefreshAddress) Description() string {
	return "Refresh read model for one or more addresses on a chain"
}

func (c *RefreshAddress) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain", Usage: "blockchain identifier (e.g. ethereum, bitcoin, solana)", Required: true},
			&command.ArgumentStringSlice{Name: "addresses", Usage: "one or more addresses to refresh", Min: 1, Max: -1},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "scope", Value: "full", Usage: "balances|transactions|tokens|utxos|full"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshAddress) Handle(ctx console.Context) error {
	out, err := c.run.RefreshAddresses(context.Background(), refresh.AddressCommand{
		Chain:     ctx.ArgumentString("chain"),
		Addresses: ctx.ArgumentStringSlice("addresses"),
		Reason:    ctx.Option("reason"),
	})
	printReport(ctx, out.Info, out.Warning, out.Line, out.SoftError)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
