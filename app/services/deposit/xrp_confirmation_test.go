package deposit

import (
	"context"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
	"github.com/macrowallets/waas/tests/feature/support/fakexrpl"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// recordedConfirmations records which withdrawals the tracker announced as confirmed.
type recordedConfirmations struct {
	confirmed []models.Transaction
}

func (r *recordedConfirmations) MarkConfirmed(_ context.Context, tx *models.Transaction) error {
	r.confirmed = append(r.confirmed, *tx)
	return nil
}

func (r *recordedConfirmations) Backfill(context.Context, int) (int, error) { return 0, nil }

func TestRunWithdrawalConfirmationCheck_XRPConfirmsOnlyValidatedSuccessAndRecordsTheFee(t *testing.T) {
	fixtures.TestDB(t)
	node := fakexrpl.New(t)
	adapter := chainfixtures.NewXRPAltnet(node.URL, func(context.Context, *types.SignedTx) (string, error) {
		t.Fatal("the confirmation tracker broadcast a payment")
		return "", nil
	})
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := fixtures.InsertWallet(t, models.ChainTXRP)
	succeeded := fixtures.InsertTransaction(t, w.ID, nil, models.ChainTXRP, models.TxTypeWithdrawal, "confirming", models.NativeXRP, "3000000", 0)
	failed := fixtures.InsertTransaction(t, w.ID, nil, models.ChainTXRP, models.TxTypeWithdrawal, "confirming", models.NativeXRP, "500000", 0)
	notValidated := fixtures.InsertTransaction(t, w.ID, nil, models.ChainTXRP, models.TxTypeWithdrawal, "confirming", models.NativeXRP, "2000000", 0)
	node.SetTx(succeeded.TxHash, fakexrpl.Tx{Validated: true, LedgerIndex: 95, Result: "tesSUCCESS", Fee: "12"})
	node.SetTx(failed.TxHash, fakexrpl.Tx{Validated: true, LedgerIndex: 96, Result: "tecNO_DST_INSUF_XRP", Fee: "12"})
	node.SetTx(notValidated.TxHash, fakexrpl.Tx{Validated: false, Result: "tesSUCCESS", Fee: "12"})

	svc := newDepositSvc(registry, newWebhookSvc())
	confirmations := &recordedConfirmations{}
	svc.SetWithdrawalConfirmations(confirmations)
	if err := svc.RunWithdrawalConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Validated ledger 100 is the tip, so ledger 95 has 6 confirmations.
	got := reloadTransaction(t, succeeded.ID)
	if got.Status != string(types.TxStatusConfirmed) || got.BlockNumber != 95 || got.Confirmations != 6 || got.Fee != "12" {
		t.Fatalf("validated tesSUCCESS: status %s block %d confirmations %d fee %q", got.Status, got.BlockNumber, got.Confirmations, got.Fee)
	}
	for name, tx := range map[string]models.Transaction{"validated tec": failed, "not validated": notValidated} {
		got := reloadTransaction(t, tx.ID)
		if got.Status != "confirming" || got.BlockNumber != 0 || got.Fee != "" {
			t.Fatalf("%s: status %s block %d fee %q, want it left confirming", name, got.Status, got.BlockNumber, got.Fee)
		}
	}
	if len(confirmations.confirmed) != 1 || confirmations.confirmed[0].ID != succeeded.ID {
		t.Fatalf("withdrawal.confirmed published for %v, want only %s", confirmations.confirmed, succeeded.ID)
	}
}
