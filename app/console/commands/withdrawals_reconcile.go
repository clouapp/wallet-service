package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/deposit"
)

// withdrawalReconciler settles one withdrawal from what the chain proves;
// *deposit.Service is the one implementation.
type withdrawalReconciler interface {
	ReconcileWithdrawal(ctx context.Context, in deposit.WithdrawalReconciliation) (deposit.WithdrawalReconcileReport, error)
}

// WithdrawalsReconcile settles a withdrawal whose transaction the tracker could not:
// an unknown broadcast outcome the chain cannot prove yet, a node without the
// transaction index or archive state. It re-reads the transaction on every endpoint
// and, for EVM, proves which transaction spent its nonce; with --apply it records
// the block (landed) or fails the withdrawal with the proof (failed).
type WithdrawalsReconcile struct {
	withdrawals withdrawalReconciler
}

// NewWithdrawalsReconcile wires the command; the service is required.
func NewWithdrawalsReconcile(withdrawals withdrawalReconciler) *WithdrawalsReconcile {
	if withdrawals == nil {
		panic("withdrawals:reconcile: deposit service is required")
	}
	return &WithdrawalsReconcile{withdrawals: withdrawals}
}

func (c *WithdrawalsReconcile) Signature() string {
	return "withdrawals:reconcile"
}

func (c *WithdrawalsReconcile) Description() string {
	return "Re-read a withdrawal's transaction on every endpoint and say whether it landed, failed (with the proof) or is still unknown; --apply records it"
}

func (c *WithdrawalsReconcile) Extend() command.Extend {
	return command.Extend{
		Category: "withdraw",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "withdrawal", Usage: "withdrawal id", Required: true},
		},
		Flags: []command.Flag{
			&command.BoolFlag{Name: "apply", Usage: "record the block of a landed transfer, or fail a transfer proven unable to land (default: print the verdict only)"},
		},
	}
}

func (c *WithdrawalsReconcile) Handle(ctx console.Context) error {
	raw := strings.TrimSpace(ctx.ArgumentString("withdrawal"))
	apply := ctx.OptionBool("apply")
	id, err := uuid.Parse(raw)
	if err != nil {
		return fail(ctx, fmt.Errorf("withdrawal id %q is not a uuid", raw))
	}
	report, err := c.withdrawals.ReconcileWithdrawal(context.Background(), deposit.WithdrawalReconciliation{WithdrawalID: id, Apply: apply})
	if err != nil {
		return fail(ctx, err)
	}
	line := redactedLine(reconcileLine(report, apply))
	if report.Verdict == deposit.ReconcileUnknown {
		ctx.Warning(line)
		return nil
	}
	ctx.Info(line)
	return nil
}

func reconcileLine(report deposit.WithdrawalReconcileReport, apply bool) string {
	verdict := report.Verdict
	if report.FailureCode != "" {
		verdict += " " + report.FailureCode
	}
	if report.Block > 0 {
		verdict += fmt.Sprintf(" at block %d", report.Block)
	}
	line := fmt.Sprintf("withdrawal %s %s tx %s (%s): %s: %s", report.WithdrawalID, report.Chain, report.TxHash, report.Status, verdict, report.Reason)
	switch {
	case report.Applied:
		return line + "; applied"
	case report.Verdict == deposit.ReconcileUnknown || report.Verdict == deposit.ReconcileSettled:
		return line + "; nothing written"
	case !apply:
		return line + "; dry run, nothing written (pass --apply)"
	default:
		return line + "; not applied"
	}
}
