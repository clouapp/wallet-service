package commands

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/macrowallets/waas/app/services/walletactivation"
)

type testWalletActivation interface {
	Recover(context.Context, walletactivation.Input) (walletactivation.Report, error)
}

// WalletsActivateTest is an operator-only repair; no activation credential is accepted or printed.
type WalletsActivateTest struct{ service testWalletActivation }

func NewWalletsActivateTest(service testWalletActivation) *WalletsActivateTest {
	if service == nil {
		panic("wallets:activate-test: activation service is required")
	}
	return &WalletsActivateTest{service: service}
}

func (c *WalletsActivateTest) Signature() string { return "wallets:activate-test" }
func (c *WalletsActivateTest) Description() string {
	return "Inspect one pending test wallet; --apply activates it through the official service without printing its code"
}
func (c *WalletsActivateTest) Extend() command.Extend {
	return command.Extend{Category: "wallets", Flags: []command.Flag{
		&command.StringFlag{Name: "account", Usage: "exact test account UUID"},
		&command.StringFlag{Name: "wallet", Usage: "exact pending wallet UUID"},
		&command.StringFlag{Name: "expect-chain", Usage: "exact configured test chain id"},
		&command.BoolFlag{Name: "apply", Usage: "activate the selected pending test wallet (default: inspect only)"},
	}}
}
func (c *WalletsActivateTest) Handle(ctx console.Context) error {
	report, err := c.service.Recover(context.Background(), walletactivation.Input{AccountID: ctx.Option("account"), WalletID: ctx.Option("wallet"), ExpectedChain: ctx.Option("expect-chain"), Apply: ctx.OptionBool("apply")})
	if err != nil {
		return fail(ctx, err)
	}
	ctx.Info(fmt.Sprintf("account=%s wallet=%s chain=%s status=%s applied=%t; no funds moved", report.AccountID, report.WalletID, report.Chain, report.Status, report.Applied))
	return nil
}
