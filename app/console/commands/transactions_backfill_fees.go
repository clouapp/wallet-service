package commands

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/container"
)

// TransactionsBackfillFees fills transactions.fee of confirmed withdrawals, sweeps
// and gas seeds recorded before the confirmation tracker stored the paid fee.
type TransactionsBackfillFees struct{}

func (c *TransactionsBackfillFees) Signature() string {
	return "transactions:backfill-fees"
}

func (c *TransactionsBackfillFees) Description() string {
	return "Read from the chain the fee paid by confirmed outbound transactions with an empty fee; dry run unless --apply"
}

func (c *TransactionsBackfillFees) Extend() command.Extend {
	return command.Extend{
		Category: "transactions",
		Flags: []command.Flag{
			&command.StringFlag{Name: "chain", Usage: "comma-separated chain ids (default: every registered chain)"},
			&command.BoolFlag{Name: "apply", Usage: "write the fees (default: print them only)"},
		},
	}
}

func (c *TransactionsBackfillFees) Handle(ctx console.Context) error {
	ctr := container.Get()
	chainIDs := ctr.Registry.ChainIDs()
	if selected := strings.TrimSpace(ctx.Option("chain")); selected != "" {
		chainIDs = nil
		for _, id := range strings.Split(selected, ",") {
			if id = strings.TrimSpace(id); id != "" {
				chainIDs = append(chainIDs, id)
			}
		}
	}
	slices.Sort(chainIDs)
	apply := ctx.OptionBool("apply")

	report, err := ctr.DepositService.BackfillPaidFees(context.Background(), chainIDs, apply)
	for _, row := range report.Rows {
		if row.Err != nil {
			ctx.Warning(fmt.Sprintf("%s %s %s: fee not read: %v", row.Chain, row.TxType, row.TxHash, row.Err))
			continue
		}
		ctx.Info(fmt.Sprintf("%s %s %s: fee %s", row.Chain, row.TxType, row.TxHash, row.Fee))
	}
	for _, id := range report.Unsupported {
		ctx.Warning(id + ": adapter cannot read paid fees, skipped")
	}
	if err != nil {
		return failCommand(ctx, err)
	}
	if !apply {
		ctx.Info(fmt.Sprintf("dry run: %d rows found, nothing written (pass --apply)", len(report.Rows)))
		return nil
	}
	ctx.Info(fmt.Sprintf("wrote %d of %d fees", report.Written, len(report.Rows)))
	return nil
}
