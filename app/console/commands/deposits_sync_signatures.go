package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/deposit"
)

// signatureSyncer runs one deposit signature sync cycle of a chain;
// *deposit.Service is the one implementation.
type signatureSyncer interface {
	SyncSignatures(ctx context.Context, chainID string) (deposit.SignatureSyncSummary, error)
}

// DepositsSyncSignatures runs one deposit signature sync cycle of a chain now: the
// deposits of every watched address read account by account since each account's
// cursor, then the chain's confirmation step. It runs whether or not the chain is in
// deposit_signatures (a one-off recovery while its block scan runs is idempotent).
type DepositsSyncSignatures struct {
	deposits signatureSyncer
}

// NewDepositsSyncSignatures wires the command; the service is required.
func NewDepositsSyncSignatures(deposits signatureSyncer) *DepositsSyncSignatures {
	if deposits == nil {
		panic("deposits:sync-signatures: deposit service is required")
	}
	return &DepositsSyncSignatures{deposits: deposits}
}

func (c *DepositsSyncSignatures) Signature() string {
	return "deposits:sync-signatures"
}

func (c *DepositsSyncSignatures) Description() string {
	return "Read the deposits of one chain account by account (Solana getSignaturesForAddress) since each account's cursor, record them and confirm them; exits 1 when an account failed"
}

func (c *DepositsSyncSignatures) Extend() command.Extend {
	return command.Extend{
		Category: "deposit",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain", Usage: "configured chain id, for example tsol", Required: true},
		},
	}
}

func (c *DepositsSyncSignatures) Handle(ctx console.Context) error {
	chainID := strings.TrimSpace(ctx.ArgumentString("chain"))
	summary, err := c.deposits.SyncSignatures(context.Background(), chainID)
	if err != nil {
		return fail(ctx, err)
	}
	if summary.Skipped {
		return fail(ctx, fmt.Errorf("signature sync skipped: another scan holds %s; run it again once that scan ends", chainID))
	}
	ctx.Info(signatureSyncLine(chainID, summary))
	if summary.FailedAccounts > 0 {
		return fail(ctx, fmt.Errorf("%d of %d %s accounts failed; their cursors stay before the failure, run the command again", summary.FailedAccounts, summary.Accounts, chainID))
	}
	return nil
}

func signatureSyncLine(chainID string, summary deposit.SignatureSyncSummary) string {
	return fmt.Sprintf("signature sync complete: chain=%s accounts=%d new_signatures=%d deposits=%d failed_accounts=%d resolved_pending=%d",
		chainID, summary.Accounts, summary.NewSignatures, summary.Deposits, summary.FailedAccounts, summary.ResolvedPending)
}
