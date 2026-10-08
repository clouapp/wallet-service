package commands

import (
	"context"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/refresh"
)

type RefreshTx struct {
	balances *refresh.BalanceService
	run      *refresh.Operator
}

// RefreshTxDeps is everything the refresh:tx command needs.
// Balances is required.
type RefreshTxDeps struct {
	Balances     *refresh.BalanceService
	Wallets      refresh.WalletLookup
	Transactions refresh.TransactionLookup
}

// NewRefreshTx refreshes the wallet that owns one transaction.
func NewRefreshTx(deps RefreshTxDeps) *RefreshTx {
	if deps.Balances == nil {
		panic("refresh:tx: balance refresh service is required")
	}
	return &RefreshTx{
		balances: deps.Balances,
		run: refresh.NewOperator(refresh.OperatorDeps{
			Balances:     deps.Balances,
			Wallets:      deps.Wallets,
			Transactions: deps.Transactions,
		}),
	}
}

func (c *RefreshTx) Signature() string { return "refresh:tx" }

func (c *RefreshTx) Description() string {
	return "Refresh read model for a single transaction"
}

func (c *RefreshTx) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain", Usage: "blockchain identifier (e.g. ethereum, bitcoin, solana)", Required: true},
			&command.ArgumentString{Name: "tx_hash", Usage: "transaction hash to refresh", Required: true},
		},
		Flags: []command.Flag{
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshTx) Handle(ctx console.Context) error {
	out, err := c.run.RefreshTransaction(context.Background(), refresh.TxCommand{
		Chain:  ctx.ArgumentString("chain"),
		TxHash: ctx.ArgumentString("tx_hash"),
		Reason: ctx.Option("reason"),
	})
	printReport(ctx, out.Info, out.Warning, out.Line, out.SoftError)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
