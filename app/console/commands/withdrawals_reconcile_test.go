package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/services/deposit"
)

type fakeWithdrawalReconciler struct {
	calls  int
	asked  deposit.WithdrawalReconciliation
	report deposit.WithdrawalReconcileReport
	err    error
}

func (f *fakeWithdrawalReconciler) ReconcileWithdrawal(_ context.Context, in deposit.WithdrawalReconciliation) (deposit.WithdrawalReconcileReport, error) {
	f.calls++
	f.asked = in
	return f.report, f.err
}

var reconciledWithdrawal = uuid.MustParse("0b7d8c1e-2f3a-4b5c-9d6e-7f8091a2b3c4")

func TestWithdrawals_Reconcile_SignatureArgumentAndFlag(t *testing.T) {
	cmd := NewWithdrawalsReconcile(&fakeWithdrawalReconciler{})
	ext := cmd.Extend()
	if cmd.Signature() != "withdrawals:reconcile" || ext.Category != "withdraw" || len(ext.Arguments) != 1 || len(ext.Flags) != 1 {
		t.Fatalf("signature %q category %q arguments %d flags %d", cmd.Signature(), ext.Category, len(ext.Arguments), len(ext.Flags))
	}
	defer func() {
		if recover() == nil {
			t.Fatal("constructor accepted a missing service")
		}
	}()
	NewWithdrawalsReconcile(nil)
}

func TestWithdrawals_Reconcile_DryRunPrintsTheVerdictAndWritesNothing(t *testing.T) {
	reconciler := &fakeWithdrawalReconciler{report: deposit.WithdrawalReconcileReport{
		WithdrawalID: reconciledWithdrawal, Chain: "teth", TxHash: "0xabc", Status: "confirming",
		Verdict: deposit.ReconcileFailed, Block: 950, FailureCode: "not_landed", Reason: "another transaction of the sender spent nonce 41 in block 950",
	}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("id").Return(" " + reconciledWithdrawal.String() + " ")
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().Info("withdrawal 0b7d8c1e-2f3a-4b5c-9d6e-7f8091a2b3c4 teth tx 0xabc (confirming): failed not_landed at block 950: another transaction of the sender spent nonce 41 in block 950; dry run, nothing written (pass --apply)").Return()

	if err := NewWithdrawalsReconcile(reconciler).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	if reconciler.calls != 1 || reconciler.asked != (deposit.WithdrawalReconciliation{ID: reconciledWithdrawal}) {
		t.Fatalf("service saw %d calls with %+v", reconciler.calls, reconciler.asked)
	}
}

func TestWithdrawals_Reconcile_ApplyReportsWhatWasRecorded(t *testing.T) {
	reconciler := &fakeWithdrawalReconciler{report: deposit.WithdrawalReconcileReport{
		WithdrawalID: reconciledWithdrawal, Chain: "teth", TxHash: "0xabc", Status: "confirming",
		Verdict: deposit.ReconcileLanded, Block: 961, Reason: "it spent nonce 41 in block 961", Applied: true,
	}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("id").Return(reconciledWithdrawal.String())
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Info("withdrawal 0b7d8c1e-2f3a-4b5c-9d6e-7f8091a2b3c4 teth tx 0xabc (confirming): landed at block 961: it spent nonce 41 in block 961; applied").Return()

	if err := NewWithdrawalsReconcile(reconciler).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	if !reconciler.asked.Apply {
		t.Fatal("--apply reached the service off")
	}
}

func TestWithdrawals_Reconcile_AnUnknownVerdictIsAWarningAndAnErrorFailsTheCommand(t *testing.T) {
	unknown := &fakeWithdrawalReconciler{report: deposit.WithdrawalReconcileReport{
		WithdrawalID: reconciledWithdrawal, Chain: "tsol", TxHash: "sig", Status: "confirming",
		Verdict: deposit.ReconcileUnknown, Reason: "no endpoint has the transaction and nothing proves yet that it cannot land",
	}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("id").Return(reconciledWithdrawal.String())
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Warning("withdrawal 0b7d8c1e-2f3a-4b5c-9d6e-7f8091a2b3c4 tsol tx sig (confirming): unknown: no endpoint has the transaction and nothing proves yet that it cannot land; nothing written").Return()
	if err := NewWithdrawalsReconcile(unknown).Handle(ctx); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("withdrawal has no transaction: nothing was sent")
	failing := &fakeWithdrawalReconciler{err: boom}
	ctx = mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("id").Return(reconciledWithdrawal.String())
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().Error(mock.Anything).Return()
	if err := NewWithdrawalsReconcile(failing).Handle(ctx); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}

	unread := &fakeWithdrawalReconciler{}
	ctx = mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("id").Return("not-a-uuid")
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().Error(mock.Anything).Return()
	if err := NewWithdrawalsReconcile(unread).Handle(ctx); err == nil || unread.calls != 0 {
		t.Fatalf("an id that is not a uuid: err %v, calls %d", err, unread.calls)
	}
}

func TestWithdrawals_Reconcile_NamesATransactionReconciledByItsOwnID(t *testing.T) {
	seed := uuid.MustParse("5d1f0c2b-3a4e-4f6a-8b9c-0d1e2f3a4b5c")
	reconciler := &fakeWithdrawalReconciler{report: deposit.WithdrawalReconcileReport{
		TransactionID: seed, Chain: "teth", TxHash: "0xseed", Status: "confirming",
		Verdict: deposit.ReconcileFailed, FailureCode: "not_landed", Block: 940, Reason: "another transaction of the sender spent nonce 11 in block 940", Applied: true,
	}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("id").Return(seed.String())
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Info("transaction 5d1f0c2b-3a4e-4f6a-8b9c-0d1e2f3a4b5c teth tx 0xseed (confirming): failed not_landed at block 940: another transaction of the sender spent nonce 11 in block 940; applied").Return()
	if err := NewWithdrawalsReconcile(reconciler).Handle(ctx); err != nil {
		t.Fatal(err)
	}
}
