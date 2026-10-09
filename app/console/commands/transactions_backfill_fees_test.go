package commands

import (
	"context"
	"errors"
	"reflect"
	"testing"

	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/services/deposit"
)

func TestBackfill_Fees_SelectsEveryRegisteredChainSortedByDefault(t *testing.T) {
	got := backfillChainIDs([]string{"tron", "eth", "btc"}, "  ")
	if want := []string{"btc", "eth", "tron"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chains = %v, want %v", got, want)
	}
}

func TestBackfill_Fees_SelectsOnlyTheNamedChains(t *testing.T) {
	got := backfillChainIDs([]string{"btc", "eth", "tron"}, " tron, ,eth ,")
	if want := []string{"eth", "tron"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chains = %v, want %v", got, want)
	}
}

type fakeFeeBackfill struct {
	chainIDs []string
	apply    bool
	report   deposit.FeeBackfillReport
	err      error
}

func (f *fakeFeeBackfill) BackfillPaidFees(_ context.Context, chainIDs []string, apply bool) (deposit.FeeBackfillReport, error) {
	f.chainIDs, f.apply = chainIDs, apply
	return f.report, f.err
}

type fakeChainIDs []string

func (f fakeChainIDs) ChainIDs() []string { return f }

func TestBackfill_Fees_ConstructorRequiresItsDependencies(t *testing.T) {
	for name, build := range map[string]func(){
		"fees":     func() { NewTransactionsBackfillFees(nil, fakeChainIDs{}) },
		"registry": func() { NewTransactionsBackfillFees(&fakeFeeBackfill{}, nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: constructor accepted a missing dependency", name)
				}
			}()
			build()
		}()
	}
}

func TestBackfill_Fees_SignatureAndFlags(t *testing.T) {
	cmd := NewTransactionsBackfillFees(&fakeFeeBackfill{}, fakeChainIDs{})
	if cmd.Signature() != "transactions:backfill-fees" || len(cmd.Extend().Flags) != 2 {
		t.Fatalf("signature %q, flags %d", cmd.Signature(), len(cmd.Extend().Flags))
	}
}

func TestBackfill_Fees_DryRunReadsTheSelectedChainsAndWritesNothing(t *testing.T) {
	fees := &fakeFeeBackfill{report: deposit.FeeBackfillReport{
		Rows: []deposit.FeeBackfillRow{{Chain: "eth", TxType: "sweep", TxHash: "0xabc", Fee: "21000"}},
	}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().OptionBool("apply").Return(false)
	ctx.EXPECT().Option("chain").Return("eth")
	ctx.EXPECT().Info("eth sweep 0xabc: fee 21000").Return()
	ctx.EXPECT().Info("dry run: 1 rows found, nothing written (pass --apply)").Return()

	err := NewTransactionsBackfillFees(fees, fakeChainIDs{"eth", "tron"}).Handle(ctx)

	if err != nil {
		t.Fatal(err)
	}
	if fees.apply || !reflect.DeepEqual(fees.chainIDs, []string{"eth"}) {
		t.Fatalf("service saw apply=%v chains=%v", fees.apply, fees.chainIDs)
	}
}

func TestBackfill_Fees_AFailedReadIsReportedAndFailsTheCommand(t *testing.T) {
	boom := errors.New("rpc down")
	fees := &fakeFeeBackfill{err: boom}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().OptionBool("apply").Return(true)
	ctx.EXPECT().Option("chain").Return("")
	ctx.EXPECT().Error(mock.Anything).Return()

	err := NewTransactionsBackfillFees(fees, fakeChainIDs{"eth"}).Handle(ctx)

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the service error", err)
	}
	if !fees.apply {
		t.Fatal("--apply did not reach the service")
	}
}
