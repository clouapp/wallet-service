package commands

import (
	"context"
	"testing"

	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/macrowallets/waas/app/services/walletactivation"
	"github.com/stretchr/testify/require"
)

type testActivationCommandService struct {
	seen  walletactivation.Input
	calls int
	err   error
}

func (s *testActivationCommandService) Recover(_ context.Context, input walletactivation.Input) (walletactivation.Report, error) {
	s.seen = input
	s.calls++
	return walletactivation.Report{AccountID: input.AccountID, WalletID: input.WalletID, Chain: input.ExpectedChain, Status: "pending", Applied: input.Apply}, s.err
}

func TestWalletsActivateTest_DefaultDryRunAndExplicitApply(t *testing.T) {
	for _, apply := range []bool{false, true} {
		service := &testActivationCommandService{}
		ctx := mocksconsole.NewContext(t)
		ctx.EXPECT().Option("account").Return("account-selected")
		ctx.EXPECT().Option("wallet").Return("wallet-selected")
		ctx.EXPECT().Option("expect-chain").Return("arbitrum")
		ctx.EXPECT().OptionBool("apply").Return(apply)
		line := "account=account-selected wallet=wallet-selected chain=arbitrum status=pending applied=false; no funds moved"
		if apply {
			line = "account=account-selected wallet=wallet-selected chain=arbitrum status=pending applied=true; no funds moved"
		}
		ctx.EXPECT().Info(line).Return()
		command := NewWalletsActivateTest(service)
		require.Equal(t, "wallets:activate-test", command.Signature())
		require.Len(t, command.Extend().Flags, 4)
		require.NoError(t, command.Handle(ctx))
		require.Equal(t, 1, service.calls)
		require.Equal(t, walletactivation.Input{AccountID: "account-selected", WalletID: "wallet-selected", ExpectedChain: "arbitrum", Apply: apply}, service.seen)
	}
}

func TestWalletsActivateTest_FailureIsNonzeroWithoutCredentialArgument(t *testing.T) {
	service := &testActivationCommandService{err: walletactivation.ErrCodeUnavailable}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Option("account").Return("account-selected")
	ctx.EXPECT().Option("wallet").Return("wallet-selected")
	ctx.EXPECT().Option("expect-chain").Return("arbitrum")
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Error(walletactivation.ErrCodeUnavailable.Error()).Return()
	require.Error(t, NewWalletsActivateTest(service).Handle(ctx))
}
