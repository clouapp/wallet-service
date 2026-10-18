package commands

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/goravel/framework/contracts/console/command"
	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/services/deposit"
)

type fakeDepositOperator struct {
	req deposit.OperatorScan
	out deposit.OperatorOutput
	err error
}

func (f *fakeDepositOperator) RunOperator(_ context.Context, req deposit.OperatorScan) (deposit.OperatorOutput, error) {
	f.req = req
	return f.out, f.err
}

// scanDepositsContext answers the options of one scan:deposits invocation.
func scanDepositsContext(t *testing.T, chainID string, setCheckpoint int64, apply bool) *mocksconsole.Context {
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().ArgumentString("chain").Return(chainID)
	ctx.EXPECT().OptionInt64("block").Return(0)
	ctx.EXPECT().Option("tx").Return("")
	ctx.EXPECT().OptionBool("pending").Return(false)
	ctx.EXPECT().OptionBool("retry-pending").Return(false)
	ctx.EXPECT().OptionInt64("set-checkpoint").Return(setCheckpoint)
	ctx.EXPECT().OptionBool("apply").Return(apply)
	return ctx
}

func TestScan_Deposits_OffersSetCheckpointAndApply(t *testing.T) {
	flags := map[string]bool{}
	for _, flag := range NewScanDeposits(&fakeDepositOperator{}).Extend().Flags {
		switch typed := flag.(type) {
		case *command.Int64Flag:
			flags[typed.Name] = true
		case *command.BoolFlag:
			flags[typed.Name] = true
		}
	}
	for _, name := range []string{"set-checkpoint", "apply"} {
		if !flags[name] {
			t.Errorf("missing flag --%s", name)
		}
	}
}

func TestScan_Deposits_SetCheckpointDryRunPrintsTheServiceReport(t *testing.T) {
	operator := &fakeDepositOperator{out: deposit.OperatorOutput{Info: []string{
		"checkpoint of btc: database 5158070, cache 5158070, head 156878",
		"dry run: would set the btc checkpoint to 156875 in the database and the cache (pass --apply)",
	}}}
	ctx := scanDepositsContext(t, " btc ", 156875, false)
	for _, line := range operator.out.Info {
		ctx.EXPECT().Info(line).Return()
	}

	if err := NewScanDeposits(operator).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	want := deposit.OperatorScan{ChainID: "btc", SetCheckpoint: 156875}
	if operator.req != want {
		t.Fatalf("service saw %+v, want %+v", operator.req, want)
	}
}

func TestScan_Deposits_SetCheckpointApplyReachesTheService(t *testing.T) {
	operator := &fakeDepositOperator{out: deposit.OperatorOutput{Info: []string{"checkpoint of btc set to 156875 in the database and the cache; the next scan resumes after block 156875"}}}
	ctx := scanDepositsContext(t, "btc", 156875, true)
	ctx.EXPECT().Info(operator.out.Info[0]).Return()

	if err := NewScanDeposits(operator).Handle(ctx); err != nil {
		t.Fatal(err)
	}
	if !operator.req.Apply || operator.req.SetCheckpoint != 156875 {
		t.Fatalf("service saw %+v, want --set-checkpoint=156875 --apply", operator.req)
	}
}

func TestScan_Deposits_ABlockPastTheHeadIsReportedAndFailsTheCommand(t *testing.T) {
	refusal := fmt.Errorf("set checkpoint failed: %w: block 200000 is past the btc head 156878", deposit.ErrCheckpointAheadOfHead)
	operator := &fakeDepositOperator{
		out: deposit.OperatorOutput{Info: []string{"checkpoint of btc: database 5158070, cache 5158070, head 156878"}},
		err: refusal,
	}
	ctx := scanDepositsContext(t, "btc", 200000, true)
	ctx.EXPECT().Info(operator.out.Info[0]).Return()
	ctx.EXPECT().Error(mock.Anything).Return()

	err := NewScanDeposits(operator).Handle(ctx)

	if !errors.Is(err, deposit.ErrCheckpointAheadOfHead) {
		t.Fatalf("err = %v, want the refusal", err)
	}
}
