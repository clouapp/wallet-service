package commands

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/services/deposit"
)

type fakeReceiptVerifier struct {
	calls  int
	check  deposit.DepositReceiptCheck
	report deposit.DepositReceiptReport
	err    error
}

func (f *fakeReceiptVerifier) VerifyDepositReceipts(_ context.Context, check deposit.DepositReceiptCheck) (deposit.DepositReceiptReport, error) {
	f.calls++
	f.check = check
	return f.report, f.err
}

var failedDepositRow = deposit.DepositReceiptRow{
	TransactionID:  uuid.MustParse("6f1c2d8e-0b5a-4c1e-9f7d-3a2b1c0d9e8f"),
	TxHash:         "0x235681c8abaf2b4b72f02fb9e32b2035c9c27b1afec6933379be2bce07dd26cb",
	ToAddress:      "0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a",
	ExternalUserID: "player-1",
	Asset:          "eth",
	Amount:         "2000000000000000",
	BlockNumber:    0xb62506,
	FailureCode:    "onchain_out_of_gas",
}

func TestDeposits_VerifyReceipts_SignatureArgumentsAndFlags(t *testing.T) {
	cmd := NewDepositsVerifyReceipts(&fakeReceiptVerifier{})
	ext := cmd.Extend()
	if cmd.Signature() != "deposits:verify-receipts" || ext.Category != "deposit" || len(ext.Arguments) != 1 || len(ext.Flags) != 2 {
		t.Fatalf("signature %q category %q arguments %d flags %d", cmd.Signature(), ext.Category, len(ext.Arguments), len(ext.Flags))
	}
}

func TestDeposits_VerifyReceipts_ConstructorRequiresTheService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("constructor accepted a missing service")
		}
	}()
	NewDepositsVerifyReceipts(nil)
}

func TestDeposits_VerifyReceipts_DryRunPrintsTheFailedDepositsAndWritesNothing(t *testing.T) {
	verifier := &fakeReceiptVerifier{report: deposit.DepositReceiptReport{
		Chain: "teth", Checked: 3, Succeeded: 2, Failed: []deposit.DepositReceiptRow{failedDepositRow},
	}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return(" teth ")
	ctx.EXPECT().Option("since").Return("2026-10-01")
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().Warning("teth deposit 6f1c2d8e-0b5a-4c1e-9f7d-3a2b1c0d9e8f tx 0x235681c8abaf2b4b72f02fb9e32b2035c9c27b1afec6933379be2bce07dd26cb block 11937030 to 0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a user player-1 amount 2000000000000000 eth: receipt failed (onchain_out_of_gas)").Return()
	ctx.EXPECT().Info("dry run: 3 confirmed native deposits checked on teth, 2 succeeded, 1 failed on chain, 0 not verified; nothing written (pass --apply)").Return()

	if err := NewDepositsVerifyReceipts(verifier).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	want := deposit.DepositReceiptCheck{ChainID: "teth", Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	if verifier.calls != 1 || verifier.check != want {
		t.Fatalf("service saw %d calls with %+v, want %+v", verifier.calls, verifier.check, want)
	}
}

func TestDeposits_VerifyReceipts_ApplyReportsTheDepositsMarkedFailed(t *testing.T) {
	marked := failedDepositRow
	marked.Marked = true
	verifier := &fakeReceiptVerifier{report: deposit.DepositReceiptReport{
		Chain: "teth", Checked: 1, Failed: []deposit.DepositReceiptRow{marked}, Marked: 1,
	}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return("teth")
	ctx.EXPECT().Option("since").Return("2026-10-17T00:00:00-03:00")
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Warning(mock.MatchedBy(func(line string) bool {
		return line == "teth deposit 6f1c2d8e-0b5a-4c1e-9f7d-3a2b1c0d9e8f tx 0x235681c8abaf2b4b72f02fb9e32b2035c9c27b1afec6933379be2bce07dd26cb block 11937030 to 0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a user player-1 amount 2000000000000000 eth: receipt failed (onchain_out_of_gas); marked failed, deposit.failed published"
	})).Return()
	ctx.EXPECT().Info("1 confirmed native deposits checked on teth, 0 succeeded, 1 failed on chain, 1 marked failed, 0 not verified").Return()

	if err := NewDepositsVerifyReceipts(verifier).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	if !verifier.check.Apply || !verifier.check.Since.Equal(time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("service saw %+v", verifier.check)
	}
}

func TestDeposits_VerifyReceipts_UnverifiedDepositsArePrintedAndFailTheCommand(t *testing.T) {
	unread := failedDepositRow
	unread.FailureCode = ""
	unread.Err = errors.New("provider unavailable")
	boom := errors.New("1 deposits could not be verified")
	verifier := &fakeReceiptVerifier{report: deposit.DepositReceiptReport{
		Chain: "teth", Checked: 1, Unverified: []deposit.DepositReceiptRow{unread},
	}, err: boom}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return("teth")
	ctx.EXPECT().Option("since").Return("")
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Warning("teth deposit 6f1c2d8e-0b5a-4c1e-9f7d-3a2b1c0d9e8f tx 0x235681c8abaf2b4b72f02fb9e32b2035c9c27b1afec6933379be2bce07dd26cb block 11937030 to 0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a user player-1 amount 2000000000000000 eth: receipt not verified: provider unavailable").Return()
	ctx.EXPECT().Error(mock.Anything).Return()

	err := NewDepositsVerifyReceipts(verifier).Handle(ctx)

	if !errors.Is(err, boom) {
		t.Fatalf("err %v, want the service error", err)
	}
	if !verifier.check.Since.IsZero() {
		t.Fatalf("a blank --since reads every deposit, service saw %v", verifier.check.Since)
	}
}

func TestDeposits_VerifyReceipts_RefusesAnUnreadableSinceWithoutCallingTheService(t *testing.T) {
	verifier := &fakeReceiptVerifier{}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return("teth")
	ctx.EXPECT().Option("since").Return("yesterday")
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Error(mock.Anything).Return()

	if err := NewDepositsVerifyReceipts(verifier).Handle(ctx); err == nil {
		t.Fatal("an unreadable --since must fail the command")
	}
	if verifier.calls != 0 {
		t.Fatal("nothing is read with an unreadable --since")
	}
}
