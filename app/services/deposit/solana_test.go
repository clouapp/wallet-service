package deposit

import (
	"context"
	"math/big"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	solFixtureSlot      = 506367800
	solFixtureHead      = 506367988
	solFixtureRecipient = "AJor34TKjdm2pAK6V7nkBmKNpnARHL7CkiwnBVTE9J6b"
	solFixtureSender    = "AMbsiP9F8YY2y8n9uFdqtw7yNZZHvTWFEWSQGHKtmkoQ"
	solFixtureSignature = "41iqE5xg9ttZAk1uZkirZsQz3GG1DJ3kcU33YUFJAyraS3MFG2BWMxweU7KUXqhDZuUmzW2bqs5L3PPE1RRQEK9r"
	solFixtureLamports  = 150_000_000
)

// solanaFixtureChain is the Solana port for deposit tests. Parsing a recorded
// block stays in the adapter tests; here the port reports that credit and slot.
func solanaFixtureChain(t *testing.T) *mocks.MockChain {
	t.Helper()
	adapter := mocks.NewMockChain(models.ChainSOL)
	adapter.NativeAssetVal = models.NativeSOL
	adapter.RequiredConfirmationsVal = 1
	adapter.GetLatestBlockFn = func(context.Context) (uint64, error) { return solFixtureHead, nil }
	adapter.ScanBlockFn = func(context.Context, uint64) ([]types.DetectedTransfer, error) {
		return []types.DetectedTransfer{{
			TxHash:      solFixtureSignature,
			BlockNumber: solFixtureSlot,
			From:        solFixtureSender,
			To:          solFixtureRecipient,
			Amount:      big.NewInt(solFixtureLamports),
			Asset:       models.NativeSOL,
		}}, nil
	}
	adapter.GetTransactionBlockFn = func(_ context.Context, txHash string) (uint64, error) {
		if txHash == solFixtureSignature {
			return solFixtureSlot, nil
		}
		return 0, nil
	}
	return adapter
}

func TestSolana_Deposit_DetectedFromRecordedBlockThenConfirmed(t *testing.T) {
	fixtures.TestDB(t)
	adapter := solanaFixtureChain(t)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := fixtures.InsertWallet(t, models.ChainSOL)
	addr := fixtures.InsertAddress(t, w.ID, models.ChainSOL, solFixtureRecipient, "user_sol", 1)
	svc := newDepositSvc(registry, newWebhookSvc())

	transfers, err := adapter.ScanBlock(context.Background(), solFixtureSlot)
	if err != nil {
		t.Fatal(err)
	}
	block := fetchedBlock{number: solFixtureSlot, transfers: transfers}
	if outcome := svc.processBlock(context.Background(), models.ChainSOL, adapter, block, adapter.ScanBlock); outcome.err != nil {
		t.Fatal(outcome.err)
	}

	var deposits []models.Transaction
	if err := facades.Orm().Query().Where("chain", models.ChainSOL).Where("tx_type", models.TxTypeDeposit).Find(&deposits); err != nil {
		t.Fatal(err)
	}
	if len(deposits) != 1 {
		t.Fatalf("expected one deposit for the watched address, got %d", len(deposits))
	}
	deposit := deposits[0]
	if deposit.TxHash != solFixtureSignature || deposit.AddressID == nil || *deposit.AddressID != addr.ID ||
		deposit.Amount != "150000000" || deposit.FromAddress != "AMbsiP9F8YY2y8n9uFdqtw7yNZZHvTWFEWSQGHKtmkoQ" ||
		deposit.BlockNumber != solFixtureSlot || deposit.Status != "pending" {
		t.Fatalf("unexpected deposit %+v", deposit)
	}

	if err := svc.updateConfirmations(context.Background(), models.ChainSOL, adapter, solFixtureSlot); err != nil {
		t.Fatal(err)
	}
	var reloaded models.Transaction
	if err := facades.Orm().Query().Find(&reloaded, deposit.ID); err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != "confirmed" || reloaded.Confirmations != 1 {
		t.Fatalf("expected confirmed once its own slot is finalized, got %s with %d", reloaded.Status, reloaded.Confirmations)
	}
}

func TestSolana_Withdrawal_SlotReconciledFromSignatureStatus(t *testing.T) {
	fixtures.TestDB(t)
	adapter := solanaFixtureChain(t)
	registry := chain.NewRegistry()
	registry.RegisterChain(adapter)
	w := fixtures.InsertWallet(t, models.ChainSOL)
	withdrawal := fixtures.InsertTransaction(t, w.ID, nil, models.ChainSOL, models.TxTypeWithdrawal, "confirming", models.NativeSOL, "20000000", 0)
	if _, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id", withdrawal.ID).
		Update(map[string]interface{}{"required_confs": 1, "tx_hash": solFixtureSignature}); err != nil {
		t.Fatal(err)
	}
	confirmations := &recordingWithdrawalConfirmations{}
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(confirmations)

	if err := svc.updateConfirmations(context.Background(), models.ChainSOL, adapter, solFixtureSlot+1); err != nil {
		t.Fatal(err)
	}

	var reloaded models.Transaction
	if err := facades.Orm().Query().Find(&reloaded, withdrawal.ID); err != nil {
		t.Fatal(err)
	}
	if reloaded.BlockNumber != solFixtureSlot || reloaded.Status != "confirmed" {
		t.Fatalf("expected slot %d and confirmed, got %d / %s", solFixtureSlot, reloaded.BlockNumber, reloaded.Status)
	}
	if reloaded.Fee != "5000" {
		t.Fatalf("paid fee %q, want the 5000 lamports of meta.fee", reloaded.Fee)
	}
	if len(confirmations.confirmed) != 1 || confirmations.confirmed[0].ID != withdrawal.ID {
		t.Fatalf("withdrawal.confirmed must be published once, got %d", len(confirmations.confirmed))
	}
}
