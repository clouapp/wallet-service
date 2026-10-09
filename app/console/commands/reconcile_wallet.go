package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/refresh"
)

type ReconcileWallet struct {
	balances *refresh.BalanceService
	run      *refresh.Operator
}

// ReconcileWalletDeps is everything the reconcile:wallet command needs.
// Balances is required.
type ReconcileWalletDeps struct {
	Balances *refresh.BalanceService
	Wallets  refresh.WalletLookup
}

// NewReconcileWallet reconciles one wallet.
func NewReconcileWallet(deps ReconcileWalletDeps) *ReconcileWallet {
	if deps.Balances == nil {
		panic("reconcile:wallet: balance refresh service is required")
	}
	return &ReconcileWallet{
		balances: deps.Balances,
		run: refresh.NewOperator(refresh.OperatorDeps{
			Balances: deps.Balances,
			Wallets:  deps.Wallets,
		}),
	}
}

func (c *ReconcileWallet) Signature() string { return "reconcile:wallet" }

func (c *ReconcileWallet) Description() string {
	return "Reconcile a wallet by comparing on-chain state with the read model"
}

func (c *ReconcileWallet) Extend() command.Extend {
	return command.Extend{
		Category: "reconcile",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "wallet_id", Usage: "wallet UUID to reconcile", Required: true},
		},
		Flags: []command.Flag{
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for reconciliation"},
		},
	}
}

func (c *ReconcileWallet) Handle(ctx console.Context) error {
	out, err := c.run.Reconcile(context.Background(), refresh.ReconcileCommand{
		WalletID: ctx.ArgumentString("wallet_id"),
		Reason:   ctx.Option("reason"),
	})
	printReport(ctx, out.Info, out.Warning, out.Line, out.SoftError)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
