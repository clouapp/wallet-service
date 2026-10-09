package commands

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/deposit"
)

// feeBackfiller reads and writes the paid fees; *deposit.Service is the one
// implementation.
type feeBackfiller interface {
	BackfillPaidFees(ctx context.Context, chainIDs []string, apply bool) (deposit.FeeBackfillReport, error)
}

// registeredChains lists the chain ids the process has adapters for.
type registeredChains interface {
	ChainIDs() []string
}

// TransactionsBackfillFees fills transactions.fee of confirmed withdrawals, sweeps
// and gas seeds recorded before the confirmation tracker stored the paid fee.
type TransactionsBackfillFees struct {
	fees     feeBackfiller
	registry registeredChains
}

// NewTransactionsBackfillFees wires the command; both dependencies are required.
func NewTransactionsBackfillFees(fees feeBackfiller, registry registeredChains) *TransactionsBackfillFees {
	if fees == nil {
		panic("transactions:backfill-fees: fee backfill service is required")
	}
	if registry == nil {
		panic("transactions:backfill-fees: chain registry is required")
	}
	return &TransactionsBackfillFees{fees: fees, registry: registry}
}

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
	apply := ctx.OptionBool("apply")

	report, err := c.fees.BackfillPaidFees(context.Background(), c.chainIDs(ctx), apply)
	printFeeBackfillRows(ctx, report)
	if err != nil {
		return fail(ctx, err)
	}
	printFeeBackfillSummary(ctx, report, apply)
	return nil
}

// chainIDs is the --chain selection of this run.
func (c *TransactionsBackfillFees) chainIDs(ctx console.Context) []string {
	return backfillChainIDs(c.registry.ChainIDs(), ctx.Option("chain"))
}

// backfillChainIDs is the sorted --chain selection, or every registered chain
// when the flag is blank.
func backfillChainIDs(registered []string, selected string) []string {
	chainIDs := registered
	if selected = strings.TrimSpace(selected); selected != "" {
		chainIDs = nil
		for _, id := range strings.Split(selected, ",") {
			if id = strings.TrimSpace(id); id != "" {
				chainIDs = append(chainIDs, id)
			}
		}
	}
	slices.Sort(chainIDs)
	return chainIDs
}

func printFeeBackfillRows(ctx console.Context, report deposit.FeeBackfillReport) {
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
}

func printFeeBackfillSummary(ctx console.Context, report deposit.FeeBackfillReport, apply bool) {
	if !apply {
		ctx.Info(fmt.Sprintf("dry run: %d rows found, nothing written (pass --apply)", len(report.Rows)))
		return
	}
	ctx.Info(fmt.Sprintf("wrote %d of %d fees", report.Written, len(report.Rows)))
}
