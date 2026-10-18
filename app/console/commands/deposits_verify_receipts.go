package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/deposit"
)

// depositReceiptVerifier re-reads the receipts of confirmed deposits;
// *deposit.Service is the one implementation.
type depositReceiptVerifier interface {
	VerifyDepositReceipts(ctx context.Context, check deposit.DepositReceiptCheck) (deposit.DepositReceiptReport, error)
}

// DepositsVerifyReceipts finds the confirmed native EVM deposits whose transaction
// was mined and failed (receipt status 0x0, no value moved), recorded by a scanner
// that did not read receipts, and with --apply marks them failed and publishes
// deposit.failed.
type DepositsVerifyReceipts struct {
	deposits depositReceiptVerifier
}

// NewDepositsVerifyReceipts wires the command; the service is required.
func NewDepositsVerifyReceipts(deposits depositReceiptVerifier) *DepositsVerifyReceipts {
	if deposits == nil {
		panic("deposits:verify-receipts: deposit service is required")
	}
	return &DepositsVerifyReceipts{deposits: deposits}
}

func (c *DepositsVerifyReceipts) Signature() string {
	return "deposits:verify-receipts"
}

func (c *DepositsVerifyReceipts) Description() string {
	return "Re-read the receipts of confirmed native EVM deposits and report those whose transaction failed on chain; --apply marks them failed and sends deposit.failed"
}

func (c *DepositsVerifyReceipts) Extend() command.Extend {
	return command.Extend{
		Category: "deposit",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain", Usage: "configured EVM chain id, for example teth", Required: true},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "since", Usage: "only deposits recorded at or after this instant (RFC 3339, or YYYY-MM-DD in UTC); default: every deposit"},
			&command.BoolFlag{Name: "apply", Usage: "mark the failed deposits failed and send deposit.failed (default: print them only)"},
		},
	}
}

func (c *DepositsVerifyReceipts) Handle(ctx console.Context) error {
	chainID := strings.TrimSpace(ctx.ArgumentString("chain"))
	sinceText := ctx.Option("since")
	apply := ctx.OptionBool("apply")
	since, err := parseSince(sinceText)
	if err != nil {
		return fail(ctx, err)
	}

	report, err := c.deposits.VerifyDepositReceipts(context.Background(), deposit.DepositReceiptCheck{ChainID: chainID, Since: since, Apply: apply})
	printDepositReceiptRows(ctx, report)
	if err != nil {
		return fail(ctx, err)
	}
	ctx.Info(depositReceiptSummary(report, apply))
	return nil
}

// parseSince reads --since: blank is no bound, otherwise RFC 3339 or a UTC date.
func parseSince(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at.UTC(), nil
	}
	if day, err := time.Parse(time.DateOnly, value); err == nil {
		return day.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("--since %q is neither RFC 3339 nor YYYY-MM-DD", value)
}

func printDepositReceiptRows(ctx console.Context, report deposit.DepositReceiptReport) {
	for _, row := range report.Failed {
		line := depositReceiptLine(report.Chain, row) + ": receipt failed (" + row.FailureCode + ")"
		switch {
		case row.Err != nil:
			line += "; not marked: " + row.Err.Error()
		case row.Marked:
			line += "; marked failed, deposit.failed published"
		}
		ctx.Warning(redactedLine(line))
	}
	for _, row := range report.Unverified {
		ctx.Warning(redactedLine(depositReceiptLine(report.Chain, row) + ": receipt not verified: " + row.Err.Error()))
	}
}

func depositReceiptLine(chainID string, row deposit.DepositReceiptRow) string {
	return fmt.Sprintf("%s deposit %s tx %s block %d to %s user %s amount %s %s",
		chainID, row.TransactionID, row.TxHash, row.BlockNumber, row.ToAddress, row.ExternalUserID, row.Amount, row.Asset)
}

func depositReceiptSummary(report deposit.DepositReceiptReport, apply bool) string {
	counts := fmt.Sprintf("%d confirmed native deposits checked on %s, %d succeeded, %d failed on chain", report.Checked, report.Chain, report.Succeeded, len(report.Failed))
	if !apply {
		return fmt.Sprintf("dry run: %s, %d not verified; nothing written (pass --apply)", counts, len(report.Unverified))
	}
	return fmt.Sprintf("%s, %d marked failed, %d not verified", counts, report.Marked, len(report.Unverified))
}
