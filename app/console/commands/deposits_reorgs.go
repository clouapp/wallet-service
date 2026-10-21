package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/deposit"
)

// reorgSettler lists the deposits whose block was reorganized away and that nothing
// resolved, and fails one an operator checked; *deposit.Service is the one
// implementation.
type reorgSettler interface {
	ReorgedDeposits(ctx context.Context, req deposit.ReorgedDepositsRequest) (deposit.ReorgedDepositsReport, error)
}

// DepositsReorgs lists the deposits the confirmation tracker leaves unconfirmed
// because their block was reorganized away and nothing proves where their
// transaction is (the health check's reorg_unresolved), and with --fail and
// --apply marks one an operator checked can never land failed (reorged_out),
// sending deposit.failed.
type DepositsReorgs struct {
	deposits reorgSettler
}

// NewDepositsReorgs wires the command; the service is required.
func NewDepositsReorgs(deposits reorgSettler) *DepositsReorgs {
	if deposits == nil {
		panic("deposits:reorgs: deposit service is required")
	}
	return &DepositsReorgs{deposits: deposits}
}

func (c *DepositsReorgs) Signature() string {
	return "deposits:reorgs"
}

func (c *DepositsReorgs) Description() string {
	return "List the deposits whose block was reorganized away and that nothing resolved; --fail=<transaction id> --apply marks one reorged_out and sends deposit.failed"
}

func (c *DepositsReorgs) Extend() command.Extend {
	return command.Extend{
		Category: "deposit",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain", Usage: "configured chain id, for example teth; default: every chain"},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "fail", Usage: "transaction id of an unresolved deposit checked to never land: mark it failed (reorged_out)"},
			&command.BoolFlag{Name: "apply", Usage: "with --fail, write it and send deposit.failed (default: print only)"},
		},
	}
}

func (c *DepositsReorgs) Handle(ctx console.Context) error {
	req := deposit.ReorgedDepositsRequest{ChainID: strings.TrimSpace(ctx.ArgumentString("chain"))}
	failID := strings.TrimSpace(ctx.Option("fail"))
	req.Apply = ctx.OptionBool("apply")
	if failID != "" {
		id, err := uuid.Parse(failID)
		if err != nil {
			return fail(ctx, fmt.Errorf("--fail %q is not a transaction id", failID))
		}
		req.Fail = id
	}

	report, err := c.deposits.ReorgedDeposits(context.Background(), req)
	for _, row := range report.Rows {
		ctx.Warning(redactedLine(reorgedDepositLine(row)))
	}
	if err != nil {
		return fail(ctx, err)
	}
	ctx.Info(reorgedSummary(req, report))
	return nil
}

func reorgedDepositLine(row deposit.ReorgedDepositRow) string {
	return fmt.Sprintf("%s deposit %s tx %s block %d (%s) to %s user %s amount %s %s: reorganized away since %s, next check %s",
		row.Chain, row.TransactionID, row.TxHash, row.BlockNumber, row.BlockHash, row.ToAddress, row.ExternalUserID, row.Amount, row.Asset,
		row.SeenAt.UTC().Format(time.RFC3339), row.NextCheckAt.UTC().Format(time.RFC3339))
}

func reorgedSummary(req deposit.ReorgedDepositsRequest, report deposit.ReorgedDepositsReport) string {
	switch {
	case req.Fail == uuid.Nil:
		chain := req.ChainID
		if chain == "" {
			chain = "every chain"
		}
		return fmt.Sprintf("%d reorganized deposits unresolved on %s", len(report.Rows), chain)
	case report.Failed:
		return "marked failed (reorged_out), deposit.failed published"
	default:
		return "dry run: the deposit would be marked failed (reorged_out) and deposit.failed sent; nothing written (pass --apply)"
	}
}
