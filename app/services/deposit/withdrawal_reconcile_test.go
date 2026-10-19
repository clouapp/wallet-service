package deposit

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/chainfixtures"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

// reconcileFixture is a Sepolia chain on a fake node with the reconciliation wired.
type reconcileFixture struct {
	node     *mocks.FakeEVMNode
	svc      *Service
	walletID uuid.UUID
}

func newReconcileFixture(t *testing.T) reconcileFixture {
	t.Helper()
	fixtures.TestDB(t)
	node := mocks.NewFakeEVMNode(t)
	node.BlockNumberHex = fmt.Sprintf("0x%x", notLandedHead)
	if node.Blocks == nil {
		node.Blocks = map[uint64][]mocks.FakeEVMBlockTx{}
	}
	registry := chain.NewRegistry()
	registry.RegisterChain(chainfixtures.NewEVMSepolia(node.URL()))
	w := fixtures.InsertWallet(t, models.ChainTETH)
	fixtures.InsertWebhookConfig(t, "https://hooks.example.test/evm", "secret", []string{
		string(types.EventWithdrawalConfirmed), string(types.EventWithdrawalFailed),
	})
	svc := newDepositSvc(registry, newWebhookSvc())
	svc.SetWithdrawalConfirmations(newWithdrawalPublisher())
	svc.SetWithdrawalReconciliation(ormRows{}, ormTransactions{})
	return reconcileFixture{node: node, svc: svc, walletID: w.ID}
}

// ormRows reads withdrawals and transactions straight from the test database.
type ormRows struct{}

func (ormRows) FindByID(_ context.Context, id uuid.UUID) (*models.Withdrawal, error) {
	var withdrawal models.Withdrawal
	if err := facades.Orm().Query().Where("id = ?", id).FirstOrFail(&withdrawal); err != nil {
		return nil, err
	}
	return &withdrawal, nil
}

type ormTransactions struct{}

func (ormTransactions) FindByID(_ context.Context, id uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	if err := facades.Orm().Query().Where("id = ?", id).FirstOrFail(&tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

func (f reconcileFixture) reconcile(t *testing.T, withdrawalID uuid.UUID, apply bool) WithdrawalReconcileReport {
	t.Helper()
	report, err := f.svc.ReconcileWithdrawal(context.Background(), WithdrawalReconciliation{WithdrawalID: withdrawalID, Apply: apply})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestReconcile_Withdrawal_ALandedTransferGetsItsBlock(t *testing.T) {
	f := newReconcileFixture(t)
	tx, withdrawal := sentWithdrawal(t, f.walletID, notLandedSender(1), 7, notLandedHead-100, true)
	f.node.Receipts[strings.ToLower(tx.TxHash)] = mocks.FakeEVMReceipt{
		Block: notLandedHead - 30, Status: "0x1", Gas: 21_000, GasUsed: 21_000, EffectiveGasPrice: failedReceiptGasPrice,
	}

	dry := f.reconcile(t, withdrawal.ID, false)
	if dry.Verdict != ReconcileLanded || dry.Block != notLandedHead-30 || dry.Applied || dry.TxHash != tx.TxHash {
		t.Fatalf("dry run %+v", dry)
	}
	if got := reloadTransaction(t, tx.ID); got.BlockNumber != 0 {
		t.Fatalf("a dry run wrote block %d", got.BlockNumber)
	}

	applied := f.reconcile(t, withdrawal.ID, true)
	if applied.Verdict != ReconcileLanded || !applied.Applied {
		t.Fatalf("apply %+v", applied)
	}
	if got := reloadTransaction(t, tx.ID); got.BlockNumber != int64(notLandedHead-30) || got.Status != string(types.TxStatusConfirming) {
		t.Fatalf("status %s block %d: the block is recorded and the tracker counts its confirmations", got.Status, got.BlockNumber)
	}
}

func TestReconcile_Withdrawal_FailsOnlyWithAProofItCannotLand(t *testing.T) {
	f := newReconcileFixture(t)
	f.node.NoTxIndex = true
	// A broadcast the node had accepted (no unknown outcome): the operator's check
	// still proves it from the transaction that spent its nonce.
	tx, withdrawal := sentWithdrawal(t, f.walletID, notLandedSender(1), 41, notLandedHead-100, false)
	spendNonce(f.node, notLandedHead-40, notLandedSender(1), "0x00000000000000000000000000000000000000000000000000000000000000e1", 41, 21_000)

	dry := f.reconcile(t, withdrawal.ID, false)
	if dry.Verdict != ReconcileFailed || dry.FailureCode != models.WithdrawalFailureNotLanded || dry.Block != notLandedHead-40 || dry.Applied {
		t.Fatalf("dry run %+v", dry)
	}
	if got := reloadWithdrawal(t, withdrawal.ID); got.Status != models.WithdrawalStatusBroadcast {
		t.Fatalf("a dry run failed the withdrawal: %+v", got)
	}

	applied := f.reconcile(t, withdrawal.ID, true)
	if !applied.Applied {
		t.Fatalf("apply %+v", applied)
	}
	if got := reloadTransaction(t, tx.ID); got.Status != string(types.TxStatusFailed) || got.ErrorMessage != models.WithdrawalFailureNotLanded {
		t.Fatalf("status %s error %q", got.Status, got.ErrorMessage)
	}
	if got := reloadWithdrawal(t, withdrawal.ID); got.Status != models.WithdrawalStatusFailed {
		t.Fatalf("withdrawal %+v", got)
	}
	if got := webhookEvents(t, string(types.EventWithdrawalFailed), withdrawal.ID); len(got) != 1 {
		t.Fatalf("%d withdrawal.failed events, want 1", len(got))
	}
	if again := f.reconcile(t, withdrawal.ID, true); again.Verdict != ReconcileSettled || again.Applied {
		t.Fatalf("a settled withdrawal is left as it is: %+v", again)
	}
}

func TestReconcile_Withdrawal_SaysWhyItIsStillUnknown(t *testing.T) {
	f := newReconcileFixture(t)
	f.node.NoTxIndex = true
	free, freeWithdrawal := sentWithdrawal(t, f.walletID, notLandedSender(1), 5, notLandedHead-100, true)
	_, archiveWithdrawal := sentWithdrawal(t, f.walletID, notLandedSender(2), 6, notLandedHead-100, true)
	spendNonce(f.node, notLandedHead-40, notLandedSender(2), "0x00000000000000000000000000000000000000000000000000000000000000e2", 6, 21_000)
	f.node.MissingStateBelow = notLandedHead - 13

	report := f.reconcile(t, freeWithdrawal.ID, true)
	if report.Verdict != ReconcileUnknown || report.Applied || report.Reason == "" {
		t.Fatalf("free nonce %+v", report)
	}
	if got := reloadTransaction(t, free.ID); got.Status != string(types.TxStatusConfirming) {
		t.Fatalf("status %s", got.Status)
	}
	report = f.reconcile(t, archiveWithdrawal.ID, true)
	if report.Verdict != ReconcileUnknown || !strings.Contains(report.Reason, "archive") {
		t.Fatalf("missing state %+v; the reason names the archive endpoint it needs", report)
	}
}

func TestReconcile_Withdrawal_WithoutATransactionWasNeverSent(t *testing.T) {
	f := newReconcileFixture(t)
	withdrawal := models.Withdrawal{
		ID: uuid.New(), WalletID: f.walletID, Status: models.WithdrawalStatusFailed,
		Amount: "1", DestinationAddress: "0xa9c37e6b4fd689a1daea4a144a45cfbb826ff80a", FeeEstimate: "0",
	}
	if err := facades.Orm().Query().Create(&withdrawal); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReconcileWithdrawal(context.Background(), WithdrawalReconciliation{WithdrawalID: withdrawal.ID}); err == nil {
		t.Fatal("a withdrawal without a transaction has nothing to reconcile")
	}
	if _, err := f.svc.ReconcileWithdrawal(context.Background(), WithdrawalReconciliation{WithdrawalID: uuid.New()}); err == nil {
		t.Fatal("an unknown withdrawal is an error")
	}
}
