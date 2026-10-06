package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/refresh"
)

type RefreshWallet struct {
	balances   *refresh.BalanceService
	dispatcher refresh.Dispatcher
	run        *refresh.Operator
}

// RefreshWalletDeps is everything the refresh:wallet command needs.
// Balances and Dispatcher are required.
type RefreshWalletDeps struct {
	Balances       *refresh.BalanceService
	Dispatcher     refresh.Dispatcher
	Wallets        refresh.WalletLookup
	RequestRefresh func(walletID, chainID string) error
}

// NewRefreshWallet refreshes one wallet's read model.
func NewRefreshWallet(deps RefreshWalletDeps) *RefreshWallet {
	if deps.Balances == nil {
		panic("refresh:wallet: balance refresh service is required")
	}
	if deps.Dispatcher == nil {
		panic("refresh:wallet: refresh dispatcher is required")
	}
	return &RefreshWallet{
		balances:   deps.Balances,
		dispatcher: deps.Dispatcher,
		run: refresh.NewOperator(refresh.OperatorDeps{
			Balances:       deps.Balances,
			Dispatcher:     deps.Dispatcher,
			Wallets:        deps.Wallets,
			RequestRefresh: deps.RequestRefresh,
		}),
	}
}

func (c *RefreshWallet) Signature() string { return "refresh:wallet" }

func (c *RefreshWallet) Description() string {
	return "Refresh a wallet read model (sync-first by default)"
}

func (c *RefreshWallet) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "wallet_id", Usage: "wallet UUID to refresh", Required: true},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "scope", Value: "full", Usage: "balances|transactions|tokens|utxos|full"},
			&command.StringFlag{Name: "chain", Usage: "chain override"},
			&command.BoolFlag{Name: "queue", Usage: "dispatch to queue instead of sync execution"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshWallet) Handle(ctx console.Context) error {
	out, err := c.run.RefreshWallet(context.Background(), refresh.WalletCommand{
		WalletID: ctx.ArgumentString("wallet_id"),
		Scope:    ctx.Option("scope"),
		Chain:    ctx.Option("chain"),
		Queue:    ctx.OptionBool("queue"),
		Reason:   ctx.Option("reason"),
	})
	printReport(ctx, out.Info, out.Warning, out.Line, out.SoftError)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
