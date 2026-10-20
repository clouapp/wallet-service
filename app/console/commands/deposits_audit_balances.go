package commands

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/balanceaudit"
)

// balanceAuditor audits the balances of deposit addresses; *balanceaudit.Service is
// the one implementation.
type balanceAuditor interface {
	AuditBalances(ctx context.Context, audit balanceaudit.Audit) (balanceaudit.Report, error)
}

// defaultAuditSpacing is the wait between two balance reads unless --spacing-ms says otherwise.
const defaultAuditSpacing = 250 * time.Millisecond

// DepositsAuditBalances compares, read-only, the on-chain balance of each deposit
// address of a chain with what its recorded transactions explain, and reports the
// differences beyond dust. It exits 1 when one differs or cannot be verified, so a
// scheduler that runs it daily sees it; nothing schedules it by default.
type DepositsAuditBalances struct {
	audits balanceAuditor
}

// NewDepositsAuditBalances wires the command; the service is required.
func NewDepositsAuditBalances(audits balanceAuditor) *DepositsAuditBalances {
	if audits == nil {
		panic("deposits:audit-balances: balance audit service is required")
	}
	return &DepositsAuditBalances{audits: audits}
}

func (c *DepositsAuditBalances) Signature() string {
	return "deposits:audit-balances"
}

func (c *DepositsAuditBalances) Description() string {
	return "Read-only: compare the on-chain balance of each deposit address of a chain with its credited deposits minus its recorded sweeps, withdrawals and fees, and report the differences beyond dust"
}

func (c *DepositsAuditBalances) Extend() command.Extend {
	return command.Extend{
		Category: "deposit",
		Arguments: []command.Argument{
			&command.ArgumentString{Name: "chain", Usage: "configured chain id, for example teth", Required: true},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "account", Usage: "only the addresses of this account's wallets (account id)"},
			&command.StringFlag{Name: "after", Usage: "resume after this address id (printed when a limit stopped the previous run)"},
			&command.IntFlag{Name: "limit", Value: balanceaudit.DefaultLimit, Usage: fmt.Sprintf("addresses to audit, at most %d", balanceaudit.MaxLimit)},
			&command.IntFlag{Name: "max-reads", Value: balanceaudit.DefaultMaxReads, Usage: "balance reads from the chain at most; an address is read whole or not at all"},
			&command.IntFlag{Name: "spacing-ms", Value: int(defaultAuditSpacing / time.Millisecond), Usage: "milliseconds between two balance reads"},
			&command.StringFlag{Name: "dust", Usage: "tolerance of every asset in base units (default: the chain's dust threshold of each asset, else 0)"},
			&command.StringFlag{Name: "asset", Usage: "comma-separated symbols to audit (default: the native asset and every token the chain lists)"},
		},
	}
}

func (c *DepositsAuditBalances) Handle(ctx console.Context) error {
	audit, err := balanceAuditFromFlags(ctx)
	if err != nil {
		return fail(ctx, err)
	}
	report, err := c.audits.AuditBalances(context.Background(), audit)
	printBalanceAuditRows(ctx, report)
	if err != nil {
		return fail(ctx, err)
	}
	ctx.Info(balanceAuditSummary(report))
	if report.Next != uuid.Nil {
		ctx.Info(fmt.Sprintf("more addresses left: run again with --after=%s", report.Next))
	}
	if report.Mismatched > 0 || report.Unverified > 0 {
		return fail(ctx, fmt.Errorf("%d balances differ from their recorded transactions and %d could not be verified", report.Mismatched, report.Unverified))
	}
	return nil
}

// balanceAuditFromFlags reads the audit request; a malformed flag is an error.
func balanceAuditFromFlags(ctx console.Context) (balanceaudit.Audit, error) {
	audit := balanceaudit.Audit{
		ChainID:  strings.TrimSpace(ctx.ArgumentString("chain")),
		Limit:    ctx.OptionInt("limit"),
		MaxReads: ctx.OptionInt("max-reads"),
		Spacing:  time.Duration(ctx.OptionInt("spacing-ms")) * time.Millisecond,
	}
	var err error
	if audit.AccountID, err = optionalUUID(ctx.Option("account"), "--account"); err != nil {
		return audit, err
	}
	after, err := optionalUUID(ctx.Option("after"), "--after")
	if err != nil {
		return audit, err
	}
	if after != nil {
		audit.After = *after
	}
	if dust := strings.TrimSpace(ctx.Option("dust")); dust != "" {
		value, ok := new(big.Int).SetString(dust, 10)
		if !ok {
			return audit, fmt.Errorf("--dust %q is not an integer of base units", dust)
		}
		audit.Dust = value
	}
	for _, symbol := range strings.Split(ctx.Option("asset"), ",") {
		if symbol = strings.TrimSpace(symbol); symbol != "" {
			audit.Assets = append(audit.Assets, symbol)
		}
	}
	return audit, nil
}

func optionalUUID(text, flag string) (*uuid.UUID, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	id, err := uuid.Parse(text)
	if err != nil {
		return nil, errors.New(flag + " is not an id")
	}
	return &id, nil
}

// printBalanceAuditRows prints every row that is not ok: a mismatch or an unverified
// balance as a warning, an asset in flight as information.
func printBalanceAuditRows(ctx console.Context, report balanceaudit.Report) {
	for _, row := range report.Rows {
		switch row.Status {
		case balanceaudit.StatusOK:
			continue
		case balanceaudit.StatusInFlight:
			ctx.Info(redactedLine(balanceAuditLine(report.Chain, row)))
		default:
			ctx.Warning(redactedLine(balanceAuditLine(report.Chain, row)))
		}
	}
}

func balanceAuditLine(chainID string, row balanceaudit.Row) string {
	asset := row.Asset
	if row.TokenContract != "" {
		asset += " (" + row.TokenContract + ")"
	}
	line := fmt.Sprintf("%s address %s %s wallet %s %s: ", chainID, row.AddressID, row.Address, row.WalletID, asset)
	if row.OnChain == nil {
		line += fmt.Sprintf("recorded %s, on chain unknown", amountText(row.Expected))
	} else {
		line += fmt.Sprintf("on chain %s, recorded %s, difference %s (dust %s)", amountText(row.OnChain), amountText(row.Expected), signedAmount(row.Difference), amountText(row.Dust))
	}
	line += ": " + string(row.Status)
	if row.Reason != "" {
		line += ": " + row.Reason
	}
	return line
}

func amountText(value *big.Int) string {
	if value == nil {
		return "unknown"
	}
	return value.String()
}

func signedAmount(value *big.Int) string {
	if value != nil && value.Sign() > 0 {
		return "+" + value.String()
	}
	return amountText(value)
}

func balanceAuditSummary(report balanceaudit.Report) string {
	return fmt.Sprintf("%s: %d addresses audited, %d balances read: %d ok, %d mismatched, %d in flight, %d unverified; read-only, nothing written",
		report.Chain, report.Addresses, report.Reads, report.OK, report.Mismatched, report.InFlight, report.Unverified)
}
