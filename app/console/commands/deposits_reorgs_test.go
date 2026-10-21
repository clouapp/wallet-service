package commands

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	mocksconsole "github.com/goravel/framework/mocks/console"

	"github.com/macrowallets/waas/app/services/deposit"
)

type fakeReorgSettler struct {
	calls  int
	req    deposit.ReorgedDepositsRequest
	report deposit.ReorgedDepositsReport
	err    error
}

func (f *fakeReorgSettler) ReorgedDeposits(_ context.Context, req deposit.ReorgedDepositsRequest) (deposit.ReorgedDepositsReport, error) {
	f.calls++
	f.req = req
	return f.report, f.err
}

var reorgedRow = deposit.ReorgedDepositRow{
	TransactionID:  uuid.MustParse("1b2c3d4e-5f60-4718-8a9b-0c1d2e3f4a5b"),
	Chain:          "teth",
	TxHash:         "0x00000000000000000000000000000000000000000000000000000000000000e1",
	BlockNumber:    100,
	BlockHash:      "0xaa",
	ToAddress:      "0x00000000000000000000000000000000000000e0",
	ExternalUserID: "player-1",
	Asset:          "eth",
	Amount:         "5000000000000000",
	SeenAt:         time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	NextCheckAt:    time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC),
}

const reorgedLine = "teth deposit 1b2c3d4e-5f60-4718-8a9b-0c1d2e3f4a5b tx 0x00000000000000000000000000000000000000000000000000000000000000e1 block 100 (0xaa) to 0x00000000000000000000000000000000000000e0 user player-1 amount 5000000000000000 eth: reorganized away since 2026-10-20T12:00:00Z, next check 2026-10-20T13:00:00Z"

func TestDeposits_Reorgs_SignatureArgumentsAndFlags(t *testing.T) {
	cmd := NewDepositsReorgs(&fakeReorgSettler{})
	ext := cmd.Extend()
	if cmd.Signature() != "deposits:reorgs" || ext.Category != "deposit" || len(ext.Arguments) != 1 || len(ext.Flags) != 2 {
		t.Fatalf("signature %q category %q arguments %d flags %d", cmd.Signature(), ext.Category, len(ext.Arguments), len(ext.Flags))
	}
}

func TestDeposits_Reorgs_ConstructorRequiresTheService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("constructor accepted a missing service")
		}
	}()
	NewDepositsReorgs(nil)
}

func TestDeposits_Reorgs_ListsTheUnresolvedDeposits(t *testing.T) {
	settler := &fakeReorgSettler{report: deposit.ReorgedDepositsReport{Rows: []deposit.ReorgedDepositRow{reorgedRow}}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return(" teth ")
	ctx.EXPECT().Option("fail").Return("")
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().Warning(reorgedLine).Return()
	ctx.EXPECT().Info("1 reorganized deposits unresolved on teth").Return()

	if err := NewDepositsReorgs(settler).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	if settler.calls != 1 || settler.req != (deposit.ReorgedDepositsRequest{ChainID: "teth"}) {
		t.Fatalf("service saw %d calls with %+v", settler.calls, settler.req)
	}
}

func TestDeposits_Reorgs_FailIsADryRunWithoutApply(t *testing.T) {
	settler := &fakeReorgSettler{report: deposit.ReorgedDepositsReport{Rows: []deposit.ReorgedDepositRow{reorgedRow}}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return("")
	ctx.EXPECT().Option("fail").Return(reorgedRow.TransactionID.String())
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().Warning(reorgedLine).Return()
	ctx.EXPECT().Info("dry run: the deposit would be marked failed (reorged_out) and deposit.failed sent; nothing written (pass --apply)").Return()

	if err := NewDepositsReorgs(settler).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	if settler.req != (deposit.ReorgedDepositsRequest{Fail: reorgedRow.TransactionID}) {
		t.Fatalf("request %+v", settler.req)
	}
}

func TestDeposits_Reorgs_ApplyFailsTheDeposit(t *testing.T) {
	settler := &fakeReorgSettler{report: deposit.ReorgedDepositsReport{Rows: []deposit.ReorgedDepositRow{reorgedRow}, Failed: true}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return("")
	ctx.EXPECT().Option("fail").Return(reorgedRow.TransactionID.String())
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Warning(reorgedLine).Return()
	ctx.EXPECT().Info("marked failed (reorged_out), deposit.failed published").Return()

	if err := NewDepositsReorgs(settler).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	if settler.req != (deposit.ReorgedDepositsRequest{Fail: reorgedRow.TransactionID, Apply: true}) {
		t.Fatalf("request %+v", settler.req)
	}
}

func TestDeposits_Reorgs_ABadIDOrARefusalFails(t *testing.T) {
	bad := mocksconsole.NewContext(t)
	bad.EXPECT().ArgumentString("chain").Return("")
	bad.EXPECT().Option("fail").Return("not-a-uuid")
	bad.EXPECT().OptionBool("apply").Return(true)
	bad.EXPECT().Error("--fail \"not-a-uuid\" is not a transaction id").Return()
	settler := &fakeReorgSettler{}
	if err := NewDepositsReorgs(settler).Handle(bad); err == nil || settler.calls != 0 {
		t.Fatalf("err %v calls %d, want a refusal before the service", err, settler.calls)
	}

	refused := mocksconsole.NewContext(t)
	refused.EXPECT().ArgumentString("chain").Return("")
	refused.EXPECT().Option("fail").Return(reorgedRow.TransactionID.String())
	refused.EXPECT().OptionBool("apply").Return(true)
	refused.EXPECT().Error(deposit.ErrDepositNotReorganized.Error()).Return()
	if err := NewDepositsReorgs(&fakeReorgSettler{err: deposit.ErrDepositNotReorganized}).Handle(refused); !errors.Is(err, deposit.ErrDepositNotReorganized) {
		t.Fatalf("err %v", err)
	}
}
