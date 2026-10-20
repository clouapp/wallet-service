package commands

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/services/balanceaudit"
)

type fakeBalanceAuditor struct {
	calls  int
	audit  balanceaudit.Audit
	report balanceaudit.Report
	err    error
}

func (f *fakeBalanceAuditor) AuditBalances(_ context.Context, audit balanceaudit.Audit) (balanceaudit.Report, error) {
	f.calls++
	f.audit = audit
	return f.report, f.err
}

var (
	auditedAddress = uuid.MustParse("3f0c9d2e-1b4a-4c8e-9a7d-5e6f7a8b9c0d")
	auditedWallet  = uuid.MustParse("7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d")
	auditAccount   = uuid.MustParse("0b9a8c7d-6e5f-4d3c-8b2a-1f0e9d8c7b6a")
)

// auditFlags expects the flags a run reads, with the values given.
func auditFlags(ctx *mocksconsole.Context, options map[string]string, limit, maxReads, spacing int) {
	ctx.EXPECT().ArgumentString("chain").Return(" teth ")
	ctx.EXPECT().OptionBool("show-addresses").Return(options["show-addresses"] == "true").Maybe()
	ctx.EXPECT().OptionInt("limit").Return(limit)
	ctx.EXPECT().OptionInt("max-reads").Return(maxReads)
	ctx.EXPECT().OptionInt("spacing-ms").Return(spacing)
	for _, name := range []string{"account", "after", "dust", "asset"} {
		ctx.EXPECT().Option(name).Return(options[name]).Maybe()
	}
}

func TestDeposits_AuditBalances_SignatureArgumentsAndFlags(t *testing.T) {
	cmd := NewDepositsAuditBalances(&fakeBalanceAuditor{})
	ext := cmd.Extend()
	if cmd.Signature() != "deposits:audit-balances" || ext.Category != "deposit" || len(ext.Arguments) != 1 || len(ext.Flags) != 8 {
		t.Fatalf("signature %q category %q arguments %d flags %d", cmd.Signature(), ext.Category, len(ext.Arguments), len(ext.Flags))
	}
}

func TestDeposits_AuditBalances_ConstructorRequiresTheService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("constructor accepted a missing service")
		}
	}()
	NewDepositsAuditBalances(nil)
}

func TestDeposits_AuditBalances_PassesTheFlagsAndExitsCleanWhenEveryBalanceIsExplained(t *testing.T) {
	auditor := &fakeBalanceAuditor{report: balanceaudit.Report{
		Chain: "teth", Addresses: 2, Reads: 4, OK: 3, InFlight: 1,
		Rows: []balanceaudit.Row{
			{AddressID: auditedAddress, WalletID: auditedWallet, Address: "0xchild", Asset: "ETH", Status: balanceaudit.StatusOK},
			{
				AddressID: auditedAddress, WalletID: auditedWallet, Address: "0xchild", Asset: "USDC", TokenContract: "0x1c7D",
				OnChain: big.NewInt(0), Expected: big.NewInt(0), Difference: big.NewInt(0), Dust: big.NewInt(0),
				Status: balanceaudit.StatusInFlight, Reason: "1 transactions still pending or confirming",
			},
		},
	}}
	ctx := mocksconsole.NewContext(t)
	auditFlags(ctx, map[string]string{"account": auditAccount.String(), "dust": "10", "asset": "eth, usdc,"}, 50, 200, 100)
	ctx.EXPECT().Info("teth address 3f0c9d2e-1b4a-4c8e-9a7d-5e6f7a8b9c0d wallet 7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d USDC (0x1c7D): on chain 0, recorded 0, difference 0 (dust 0): in_flight: 1 transactions still pending or confirming").Return()
	ctx.EXPECT().Info("teth: 2 addresses audited, 4 balances read: 3 ok, 0 mismatched, 1 in flight, 0 unverified; read-only, nothing written").Return()

	if err := NewDepositsAuditBalances(auditor).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	want := balanceaudit.Audit{ChainID: "teth", AccountID: &auditAccount, Limit: 50, MaxReads: 200, Spacing: 100 * time.Millisecond, Dust: big.NewInt(10), Assets: []string{"eth", "usdc"}}
	got := auditor.audit
	if auditor.calls != 1 || got.ChainID != want.ChainID || *got.AccountID != *want.AccountID || got.After != uuid.Nil ||
		got.Limit != want.Limit || got.MaxReads != want.MaxReads || got.Spacing != want.Spacing || got.Dust.Cmp(want.Dust) != 0 ||
		len(got.Assets) != 2 || got.Assets[0] != "eth" || got.Assets[1] != "usdc" {
		t.Fatalf("service saw %d calls with %+v, want %+v", auditor.calls, got, want)
	}
}

func TestDeposits_AuditBalances_AMismatchOrAnUnverifiedBalanceExitsNonZeroAndSaysWhereToResume(t *testing.T) {
	auditor := &fakeBalanceAuditor{report: balanceaudit.Report{
		Chain: "teth", Addresses: 1, Reads: 2, Mismatched: 1, Unverified: 1, Next: auditedAddress,
		Rows: []balanceaudit.Row{
			{
				AddressID: auditedAddress, WalletID: auditedWallet, Address: "0xchild", Asset: "ETH",
				OnChain: big.NewInt(5000), Expected: big.NewInt(0), Difference: big.NewInt(5000), Dust: big.NewInt(10),
				Status: balanceaudit.StatusMismatch, Reason: "the chain holds more than the recorded transactions explain (a deposit no row records)",
			},
			{
				AddressID: auditedAddress, WalletID: auditedWallet, Address: "0xchild", Asset: "USDC", TokenContract: "0x1c7D",
				Expected: big.NewInt(7), Status: balanceaudit.StatusUnverified, Reason: "balance not read: rpc 429",
			},
		},
	}}
	ctx := mocksconsole.NewContext(t)
	auditFlags(ctx, map[string]string{"after": auditedWallet.String(), "show-addresses": "true"}, 100, 500, 250)
	ctx.EXPECT().Warning("teth address 3f0c9d2e-1b4a-4c8e-9a7d-5e6f7a8b9c0d 0xchild wallet 7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d ETH: on chain 5000, recorded 0, difference +5000 (dust 10): mismatch: the chain holds more than the recorded transactions explain (a deposit no row records)").Return()
	ctx.EXPECT().Warning("teth address 3f0c9d2e-1b4a-4c8e-9a7d-5e6f7a8b9c0d 0xchild wallet 7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d USDC (0x1c7D): recorded 7, on chain unknown: unverified: balance not read: rpc 429").Return()
	ctx.EXPECT().Info("teth: 1 addresses audited, 2 balances read: 0 ok, 1 mismatched, 0 in flight, 1 unverified; read-only, nothing written").Return()
	ctx.EXPECT().Info("a mismatch can be a deposit in blocks the scanner had not reached yet: run again once it caught up before acting on one").Return()
	ctx.EXPECT().Info("more addresses left: run again with --after=3f0c9d2e-1b4a-4c8e-9a7d-5e6f7a8b9c0d").Return()
	ctx.EXPECT().Error("1 balances differ from their recorded transactions and 1 could not be verified").Return()

	err := NewDepositsAuditBalances(auditor).Handle(ctx)
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 1 {
		t.Fatalf("err %v, want exit status 1 for a mismatch", err)
	}
	if auditor.audit.After != auditedWallet {
		t.Fatalf("resumed after %s", auditor.audit.After)
	}
}

func TestDeposits_AuditBalances_AnUnverifiedOnlyRunExitsTwo(t *testing.T) {
	auditor := &fakeBalanceAuditor{report: balanceaudit.Report{
		Chain: "teth", Addresses: 1, Reads: 1, Unverified: 1,
		Rows: []balanceaudit.Row{{
			AddressID: auditedAddress, WalletID: auditedWallet, Address: "0xchild", Asset: "ETH",
			Expected: big.NewInt(7), Status: balanceaudit.StatusUnverified, Reason: "balance not read: rpc 429",
		}},
	}}
	ctx := mocksconsole.NewContext(t)
	auditFlags(ctx, nil, 100, 500, 250)
	ctx.EXPECT().Warning("teth address 3f0c9d2e-1b4a-4c8e-9a7d-5e6f7a8b9c0d wallet 7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d ETH: recorded 7, on chain unknown: unverified: balance not read: rpc 429").Return()
	ctx.EXPECT().Info("teth: 1 addresses audited, 1 balances read: 0 ok, 0 mismatched, 0 in flight, 1 unverified; read-only, nothing written").Return()
	ctx.EXPECT().Error("0 balances differ from their recorded transactions and 1 could not be verified").Return()

	err := NewDepositsAuditBalances(auditor).Handle(ctx)
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Fatalf("err %v, want exit status 2 when nothing differs but a balance could not be verified", err)
	}
}

func TestDeposits_AuditBalances_AMalformedFlagFailsBeforeAnyRead(t *testing.T) {
	for name, options := range map[string]map[string]string{
		"account": {"account": "not-an-id"},
		"after":   {"after": "12"},
		"dust":    {"dust": "0.5"},
	} {
		t.Run(name, func(t *testing.T) {
			auditor := &fakeBalanceAuditor{}
			ctx := mocksconsole.NewContext(t)
			auditFlags(ctx, options, 100, 500, 250)
			ctx.EXPECT().Error(mock.Anything).Return()
			if err := NewDepositsAuditBalances(auditor).Handle(ctx); err == nil || auditor.calls != 0 {
				t.Fatalf("err %v after %d service calls", err, auditor.calls)
			}
		})
	}
}

func TestDeposits_AuditBalances_AServiceFailureFails(t *testing.T) {
	auditor := &fakeBalanceAuditor{err: errors.New("balanceaudit: invalid audit: tdoge is not a chain")}
	ctx := mocksconsole.NewContext(t)
	auditFlags(ctx, nil, 100, 500, 250)
	ctx.EXPECT().Error("balanceaudit: invalid audit: tdoge is not a chain").Return()
	if err := NewDepositsAuditBalances(auditor).Handle(ctx); err == nil {
		t.Fatal("expected the failure")
	}
}
