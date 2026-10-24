package commands

import (
	"context"
	"fmt"
	"testing"

	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/services/deposit"
)

type fakeSignatureSyncer struct {
	calls   int
	chainID string
	summary deposit.SignatureSyncSummary
	err     error
}

func (f *fakeSignatureSyncer) SyncSignatures(_ context.Context, chainID string) (deposit.SignatureSyncSummary, error) {
	f.calls++
	f.chainID = chainID
	return f.summary, f.err
}

func TestDeposits_SyncSignatures_SignatureAndArgument(t *testing.T) {
	cmd := NewDepositsSyncSignatures(&fakeSignatureSyncer{})
	ext := cmd.Extend()
	assert.Equal(t, "deposits:sync-signatures", cmd.Signature())
	assert.Equal(t, "deposit", ext.Category)
	assert.Len(t, ext.Arguments, 1)
	assert.Empty(t, ext.Flags)
}

func TestDeposits_SyncSignatures_ConstructorRequiresTheService(t *testing.T) {
	assert.Panics(t, func() { NewDepositsSyncSignatures(nil) })
}

func TestDeposits_SyncSignatures_RunsOneCycleOfTheChainAndPrintsItsSummary(t *testing.T) {
	syncer := &fakeSignatureSyncer{summary: deposit.SignatureSyncSummary{Accounts: 36, NewSignatures: 12, Deposits: 3, ResolvedPending: 2}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return(" tsol ")
	ctx.EXPECT().Info("signature sync complete: chain=tsol accounts=36 new_signatures=12 deposits=3 failed_accounts=0 resolved_pending=2").Return()

	require.NoError(t, NewDepositsSyncSignatures(syncer).Handle(ctx))

	assert.Equal(t, 1, syncer.calls)
	assert.Equal(t, "tsol", syncer.chainID)
}

func TestDeposits_SyncSignatures_ExitsNonZeroWhenAnAccountFailed(t *testing.T) {
	syncer := &fakeSignatureSyncer{summary: deposit.SignatureSyncSummary{Accounts: 36, FailedAccounts: 2}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return("sol")
	ctx.EXPECT().Info("signature sync complete: chain=sol accounts=36 new_signatures=0 deposits=0 failed_accounts=2 resolved_pending=0").Return()
	ctx.EXPECT().Error("2 of 36 sol accounts failed; their cursors stay before the failure, run the command again").Return()

	require.Error(t, NewDepositsSyncSignatures(syncer).Handle(ctx))
}

func TestDeposits_SyncSignatures_ExitsNonZeroWhenSkippedOrFailed(t *testing.T) {
	t.Run("another scan holds the chain", func(t *testing.T) {
		ctx := mocksconsole.NewContext(t)
		ctx.EXPECT().ArgumentString("chain").Return("sol")
		ctx.EXPECT().Error("signature sync skipped: another scan holds sol; run it again once that scan ends").Return()

		require.Error(t, NewDepositsSyncSignatures(&fakeSignatureSyncer{summary: deposit.SignatureSyncSummary{Skipped: true}}).Handle(ctx))
	})
	t.Run("the cycle failed", func(t *testing.T) {
		ctx := mocksconsole.NewContext(t)
		ctx.EXPECT().ArgumentString("chain").Return("eth")
		ctx.EXPECT().Error("deposit: the chain's adapter cannot read deposits by account signatures: eth").Return()

		err := NewDepositsSyncSignatures(&fakeSignatureSyncer{err: fmt.Errorf("%w: eth", deposit.ErrSignaturesUnsupported)}).Handle(ctx)
		require.ErrorIs(t, err, deposit.ErrSignaturesUnsupported)
	})
}
