package deposit

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/goravel/framework/facades"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/mocks"
)

// feeReadingChain is a mock adapter that reports the fee each transaction paid.
type feeReadingChain struct {
	*mocks.MockChain
	fees   map[string]*big.Int
	err    error
	lookup []string
}

func (c *feeReadingChain) TransactionFee(_ context.Context, txHash string) (*big.Int, error) {
	c.lookup = append(c.lookup, txHash)
	if c.err != nil {
		return nil, c.err
	}
	return c.fees[txHash], nil
}

var _ chain.TransactionFeeReader = (*feeReadingChain)(nil)

const (
	paidFeeTestBlock    = 100
	paidFeeConfirmedTip = 102 // 3 confirmations, the mock's requirement
	paidFeeConfirming   = 101
)

func reloadTransaction(t *testing.T, id uuid.UUID) models.Transaction {
	t.Helper()
	var reloaded models.Transaction
	if err := facades.Orm().Query().Find(&reloaded, id); err != nil {
		t.Fatalf("find transaction: %v", err)
	}
	return reloaded
}

func TestUpdateConfirmations_RecordsTheFeePaidByOutboundTransactions(t *testing.T) {
	mocks.TestDB(t)
	adapter := &feeReadingChain{MockChain: mocks.NewMockChain("eth"), fees: map[string]*big.Int{}}
	adapter.RequiredConfirmationsVal = 3
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := mocks.InsertWallet(t, "eth")

	withdrawal := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", paidFeeTestBlock)
	sweep := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeSweep, "confirming", "eth", "2000", paidFeeTestBlock)
	gasSeed := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeGasSeed, "confirming", "eth", "300", paidFeeTestBlock)
	deposit := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeDeposit, "confirming", "eth", "5000", paidFeeTestBlock)
	adapter.fees[withdrawal.TxHash] = big.NewInt(21_000 * 1_500_000_000)
	adapter.fees[sweep.TxHash] = big.NewInt(42_000)
	adapter.fees[gasSeed.TxHash] = big.NewInt(0)

	svc := newDepositSvc(registry, newWebhookSvc())
	if err := svc.updateConfirmations(context.Background(), "eth", adapter, paidFeeConfirming); err != nil {
		t.Fatal(err)
	}
	if len(adapter.lookup) != 0 {
		t.Fatalf("fee read before confirmation: %v", adapter.lookup)
	}

	if err := svc.updateConfirmations(context.Background(), "eth", adapter, paidFeeConfirmedTip); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		tx   models.Transaction
		want string
	}{
		{"withdrawal", withdrawal, "31500000000000"},
		{"sweep", sweep, "42000"},
		{"gas seed with a zero fee", gasSeed, "0"},
		{"deposit, paid by its sender", deposit, ""},
	} {
		got := reloadTransaction(t, tc.tx.ID)
		if got.Status != "confirmed" || got.Fee != tc.want {
			t.Errorf("%s: status %s fee %q, want confirmed fee %q", tc.name, got.Status, got.Fee, tc.want)
		}
	}
	if len(adapter.lookup) != 3 {
		t.Fatalf("lookups = %v, want the three outbound rows only", adapter.lookup)
	}
}

func TestUpdateConfirmations_AFailedFeeLookupStillConfirms(t *testing.T) {
	mocks.TestDB(t)
	adapter := &feeReadingChain{MockChain: mocks.NewMockChain("eth"), err: errors.New("receipt unavailable")}
	adapter.RequiredConfirmationsVal = 3
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := mocks.InsertWallet(t, "eth")
	sweep := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeSweep, "confirming", "eth", "2000", paidFeeTestBlock)

	svc := newDepositSvc(registry, newWebhookSvc())
	if err := svc.updateConfirmations(context.Background(), "eth", adapter, paidFeeConfirmedTip); err != nil {
		t.Fatal(err)
	}
	got := reloadTransaction(t, sweep.ID)
	if got.Status != "confirmed" || got.Fee != "" {
		t.Fatalf("status %s fee %q, want confirmed with the fee left for the backfill", got.Status, got.Fee)
	}
}

func TestUpdateConfirmations_KeepsARecordedFee(t *testing.T) {
	mocks.TestDB(t)
	adapter := &feeReadingChain{MockChain: mocks.NewMockChain("eth"), fees: map[string]*big.Int{}}
	adapter.RequiredConfirmationsVal = 3
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := mocks.InsertWallet(t, "eth")
	withdrawal := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", paidFeeTestBlock)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", withdrawal.ID).Update("fee", "777"); err != nil {
		t.Fatal(err)
	}

	svc := newDepositSvc(registry, newWebhookSvc())
	if err := svc.updateConfirmations(context.Background(), "eth", adapter, paidFeeConfirmedTip); err != nil {
		t.Fatal(err)
	}
	if got := reloadTransaction(t, withdrawal.ID); got.Fee != "777" || len(adapter.lookup) != 0 {
		t.Fatalf("fee %q lookups %v, want the recorded fee untouched", got.Fee, adapter.lookup)
	}
}

func TestBackfillPaidFees_FillsOnlyMissingFeesAndIsIdempotent(t *testing.T) {
	mocks.TestDB(t)
	adapter := &feeReadingChain{MockChain: mocks.NewMockChain("eth"), fees: map[string]*big.Int{}}
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := mocks.InsertWallet(t, "eth")
	sweep := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeSweep, "confirmed", "eth", "2000", paidFeeTestBlock)
	gasSeed := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeGasSeed, "confirmed", "eth", "300", paidFeeTestBlock)
	pending := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeWithdrawal, "confirming", "eth", "1000", paidFeeTestBlock)
	deposit := mocks.InsertTransaction(t, w.ID, nil, "eth", models.TxTypeDeposit, "confirmed", "eth", "5000", paidFeeTestBlock)
	adapter.fees[sweep.TxHash] = big.NewInt(42_000)
	adapter.fees[gasSeed.TxHash] = big.NewInt(21_000)
	svc := newDepositSvc(registry, newWebhookSvc())

	dry, err := svc.BackfillPaidFees(context.Background(), []string{"eth"}, false)
	if err != nil || len(dry.Rows) != 2 || dry.Written != 0 {
		t.Fatalf("dry run = %+v, %v; want 2 rows found, none written", dry, err)
	}
	if got := reloadTransaction(t, sweep.ID); got.Fee != "" {
		t.Fatalf("dry run wrote fee %q", got.Fee)
	}

	applied, err := svc.BackfillPaidFees(context.Background(), []string{"eth"}, true)
	if err != nil || applied.Written != 2 {
		t.Fatalf("apply = %+v, %v", applied, err)
	}
	for id, want := range map[uuid.UUID]string{sweep.ID: "42000", gasSeed.ID: "21000", pending.ID: "", deposit.ID: ""} {
		if got := reloadTransaction(t, id); got.Fee != want {
			t.Errorf("row %s fee %q, want %q", id, got.Fee, want)
		}
	}

	again, err := svc.BackfillPaidFees(context.Background(), []string{"eth"}, true)
	if err != nil || len(again.Rows) != 0 || again.Written != 0 {
		t.Fatalf("second pass = %+v, %v; want nothing to do", again, err)
	}
}
