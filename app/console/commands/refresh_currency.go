package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/refresh"
)

type RefreshCurrency struct {
	registry   *chainpkg.Registry
	balances   *refresh.BalanceService
	dispatcher refresh.Dispatcher
	run        *refresh.Operator
}

// RefreshCurrencyDeps is everything the refresh:currency command needs.
// Registry, Balances, and Dispatcher are required.
type RefreshCurrencyDeps struct {
	Registry   *chainpkg.Registry
	Balances   *refresh.BalanceService
	Dispatcher refresh.Dispatcher
	Wallets    refresh.WalletLookup
	Addresses  refresh.AddressLookup
}

// NewRefreshCurrency refreshes wallets that hold one currency.
func NewRefreshCurrency(deps RefreshCurrencyDeps) *RefreshCurrency {
	if deps.Registry == nil {
		panic("refresh:currency: chain registry is required")
	}
	if deps.Balances == nil {
		panic("refresh:currency: balance refresh service is required")
	}
	if deps.Dispatcher == nil {
		panic("refresh:currency: refresh dispatcher is required")
	}
	return &RefreshCurrency{
		registry:   deps.Registry,
		balances:   deps.Balances,
		dispatcher: deps.Dispatcher,
		run: refresh.NewOperator(refresh.OperatorDeps{
			Balances:   deps.Balances,
			Dispatcher: deps.Dispatcher,
			Wallets:    deps.Wallets,
			Addresses:  deps.Addresses,
			Chains:     deps.Registry,
		}),
	}
}

func (c *RefreshCurrency) Signature() string { return "refresh:currency" }

func (c *RefreshCurrency) Description() string {
	return "Refresh read model for all addresses holding a currency"
}

func (c *RefreshCurrency) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "currency", Usage: "currency symbol (e.g. eth, btc, usdt)", Required: true},
			&command.ArgumentStringSlice{Name: "addresses", Usage: "one or more addresses to refresh", Min: 1, Max: -1},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "scope", Value: "full", Usage: "balances|transactions|tokens|utxos|full"},
			&command.StringFlag{Name: "chain", Usage: "required when currency exists on multiple chains"},
			&command.BoolFlag{Name: "queue", Usage: "dispatch to queue instead of sync execution"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshCurrency) Handle(ctx console.Context) error {
	out, err := c.run.RefreshCurrency(context.Background(), refresh.CurrencyCommand{
		Currency:  ctx.ArgumentString("currency"),
		Addresses: ctx.ArgumentStringSlice("addresses"),
		Chain:     ctx.Option("chain"),
		Queue:     ctx.OptionBool("queue"),
		Reason:    ctx.Option("reason"),
	})
	printReport(ctx, out.Info, out.Warning, out.Line, out.SoftError)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
