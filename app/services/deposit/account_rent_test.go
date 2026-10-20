package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

// rentReadingChain is a mock adapter that reports the fee and the account rent each
// transaction paid, like the Solana adapter.
type rentReadingChain struct {
	feeReadingChain
	rents   map[string]*big.Int
	rentErr error
	rented  []string
}

func (c *rentReadingChain) TransactionAccountRent(_ context.Context, txHash string) (*big.Int, error) {
	c.rented = append(c.rented, txHash)
	if c.rentErr != nil {
		return nil, c.rentErr
	}
	if rent, ok := c.rents[txHash]; ok {
		return rent, nil
	}
	return big.NewInt(0), nil
}

var _ chain.AccountRentReader = (*rentReadingChain)(nil)

func newRentReadingChain() *rentReadingChain {
	adapter := &rentReadingChain{
		feeReadingChain: feeReadingChain{MockChain: mocks.NewMockChain("tsol"), fees: map[string]*big.Int{}},
		rents:           map[string]*big.Int{},
	}
	adapter.RequiredConfirmationsVal = 3
	return adapter
}

// payloadOf decodes the row's raw_payload.
func payloadOf(t *testing.T, tx models.Transaction) map[string]any {
	t.Helper()
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(tx.RawPayload), &payload); err != nil {
		t.Fatalf("raw_payload %q: %v", tx.RawPayload, err)
	}
	return payload
}

func setRawPayload(t *testing.T, tx models.Transaction, raw string) {
	t.Helper()
	if _, err := facades.Orm().Query().Exec(`UPDATE transactions SET raw_payload = ? WHERE id = ?`, raw, tx.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateConfirmations_RecordsTheAccountRentAnOutboundTransferPaid(t *testing.T) {
	fixtures.TestDB(t)
	adapter := newRentReadingChain()
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := fixtures.InsertWallet(t, "tsol")
	withdrawal := fixtures.InsertTransaction(t, w.ID, nil, "tsol", models.TxTypeWithdrawal, "confirming", "USDC", "1000000", paidFeeTestBlock)
	setRawPayload(t, withdrawal, `{"last_ledger_sequence":123}`)
	sweep := fixtures.InsertTransaction(t, w.ID, nil, "tsol", models.TxTypeSweep, "confirming", "SOL", "2000", paidFeeTestBlock)
	deposit := fixtures.InsertTransaction(t, w.ID, nil, "tsol", models.TxTypeDeposit, "confirming", "SOL", "5000", paidFeeTestBlock)
	adapter.fees[withdrawal.TxHash], adapter.fees[sweep.TxHash] = big.NewInt(5000), big.NewInt(5000)
	adapter.rents[withdrawal.TxHash] = big.NewInt(2039280)

	svc := newDepositSvc(registry, newWebhookSvc())
	if err := svc.updateConfirmations(context.Background(), "tsol", adapter, paidFeeConfirming); err != nil {
		t.Fatal(err)
	}
	if len(adapter.rented) != 0 {
		t.Fatalf("rent read before confirmation: %v", adapter.rented)
	}
	if err := svc.updateConfirmations(context.Background(), "tsol", adapter, paidFeeConfirmedTip); err != nil {
		t.Fatal(err)
	}

	got := reloadTransaction(t, withdrawal.ID)
	payload := payloadOf(t, got)
	if got.Status != "confirmed" || got.Fee != "5000" || payload[chain.AccountRentPayloadKey] != "2039280" || payload["last_ledger_sequence"] != float64(123) {
		t.Fatalf("withdrawal status %s fee %q payload %v, want the rent beside the fee and the payload kept", got.Status, got.Fee, payload)
	}
	if payload := payloadOf(t, reloadTransaction(t, sweep.ID)); payload[chain.AccountRentPayloadKey] != "0" {
		t.Fatalf("sweep payload %v, want a recorded zero rent", payload)
	}
	if payload := payloadOf(t, reloadTransaction(t, deposit.ID)); payload[chain.AccountRentPayloadKey] != nil {
		t.Fatalf("deposit payload %v: its sender paid for it", payload)
	}
}

func TestUpdateConfirmations_ARentThatCannotBeReadStillConfirms(t *testing.T) {
	fixtures.TestDB(t)
	adapter := newRentReadingChain()
	adapter.rentErr = errors.New("getTransaction: rate limited")
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := fixtures.InsertWallet(t, "tsol")
	withdrawal := fixtures.InsertTransaction(t, w.ID, nil, "tsol", models.TxTypeWithdrawal, "confirming", "USDC", "1000000", paidFeeTestBlock)

	svc := newDepositSvc(registry, newWebhookSvc())
	if err := svc.updateConfirmations(context.Background(), "tsol", adapter, paidFeeConfirmedTip); err != nil {
		t.Fatal(err)
	}
	got := reloadTransaction(t, withdrawal.ID)
	if got.Status != "confirmed" || payloadOf(t, got)[chain.AccountRentPayloadKey] != nil {
		t.Fatalf("status %s payload %v, want confirmed with the rent left for the backfill", got.Status, got.RawPayload)
	}
}

func TestBackfill_PaidFees_RecordsTheAccountRentOfOlderTransfersOnce(t *testing.T) {
	fixtures.TestDB(t)
	adapter := newRentReadingChain()
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := fixtures.InsertWallet(t, "tsol")
	old := fixtures.InsertTransaction(t, w.ID, nil, "tsol", models.TxTypeWithdrawal, "confirmed", "USDC", "1000000", paidFeeTestBlock)
	if _, err := facades.Orm().Query().Exec(`UPDATE transactions SET fee = '5000' WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}
	setRawPayload(t, old, `{"broadcast_outcome":"unknown"}`)
	recorded := fixtures.InsertTransaction(t, w.ID, nil, "tsol", models.TxTypeSweep, "confirmed", "SOL", "2000", paidFeeTestBlock)
	if _, err := facades.Orm().Query().Exec(`UPDATE transactions SET fee = '5000' WHERE id = ?`, recorded.ID); err != nil {
		t.Fatal(err)
	}
	setRawPayload(t, recorded, `{"account_rent":"0"}`)
	adapter.rents[old.TxHash] = big.NewInt(2039280)
	svc := newDepositSvc(registry, newWebhookSvc())

	dry, err := svc.BackfillPaidFees(context.Background(), []string{"tsol"}, false)
	if err != nil || len(dry.Rows) != 1 || dry.Rows[0].AccountRent != "2039280" || !dry.Rows[0].Rent || dry.Written != 0 {
		t.Fatalf("dry run = %+v, %v; want the old row's rent found, nothing written", dry, err)
	}
	if payload := payloadOf(t, reloadTransaction(t, old.ID)); payload[chain.AccountRentPayloadKey] != nil {
		t.Fatalf("dry run wrote %v", payload)
	}

	applied, err := svc.BackfillPaidFees(context.Background(), []string{"tsol"}, true)
	if err != nil || applied.Written != 1 {
		t.Fatalf("apply = %+v, %v", applied, err)
	}
	payload := payloadOf(t, reloadTransaction(t, old.ID))
	if payload[chain.AccountRentPayloadKey] != "2039280" || payload["broadcast_outcome"] != "unknown" {
		t.Fatalf("payload %v, want the rent beside the keys it had", payload)
	}

	again, err := svc.BackfillPaidFees(context.Background(), []string{"tsol"}, true)
	if err != nil || len(again.Rows) != 0 {
		t.Fatalf("second pass = %+v, %v; want nothing to do", again, err)
	}
}
