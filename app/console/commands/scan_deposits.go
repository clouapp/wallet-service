package commands

import (
	"context"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/deposit"
)

type ScanDeposits struct {
	deposits *deposit.Service
}

// NewScanDeposits scans deposits for one chain.
func NewScanDeposits(deposits *deposit.Service) *ScanDeposits {
	if deposits == nil {
		panic("scan:deposits: deposit service is required")
	}
	return &ScanDeposits{deposits: deposits}
}

func (c *ScanDeposits) Signature() string { return "scan:deposits" }

func (c *ScanDeposits) Description() string {
	return "Scan new blocks for deposits on one configured chain, one block or transaction with --block / --tx, or list / retry the pending blocks with --pending / --retry-pending"
}

func (c *ScanDeposits) Extend() command.Extend {
	return command.Extend{
		Category: "deposit",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain", Usage: "configured chain id, for example teth", Required: true},
		},
		Flags: []command.Flag{
			&command.Int64Flag{Name: "block", Aliases: []string{"slot"}, Usage: "record the deposits of this block (Solana slot) only; the checkpoint is not moved"},
			&command.StringFlag{Name: "tx", Aliases: []string{"signature"}, Usage: "record the deposits of this finalized transaction only; the checkpoint is not moved"},
			&command.BoolFlag{Name: "pending", Usage: "list the blocks whose deposits failed to record and wait for a retry"},
			&command.BoolFlag{Name: "retry-pending", Usage: "retry every pending block now, ignoring its backoff"},
		},
	}
}

func (c *ScanDeposits) Handle(ctx console.Context) error {
	out, err := c.deposits.RunOperator(context.Background(), deposit.OperatorScan{
		ChainID:      strings.TrimSpace(ctx.ArgumentString("chain")),
		Block:        ctx.OptionInt64("block"),
		Tx:           ctx.Option("tx"),
		ListPending:  ctx.OptionBool("pending"),
		RetryPending: ctx.OptionBool("retry-pending"),
	})
	printReport(ctx, out.Info, nil, out.Line, nil)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}
