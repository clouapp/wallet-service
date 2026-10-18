package deposit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

func TestVerify_DepositReceipts_DryRunReportsAndApplyFailsTheConfirmedDepositOnce(t *testing.T) {
	f := newFailedDepositFixture(t)
	failed := f.insertNativeDeposit(t, string(types.TxStatusConfirmed), "")
	succeeded := f.insertNativeDeposit(t, string(types.TxStatusConfirmed), "")
	token := f.insertNativeDeposit(t, string(types.TxStatusConfirmed), "0x1c7d4b196cb0c7b01d743fbc6116a902379c7238")
	old := f.insertNativeDeposit(t, string(types.TxStatusConfirmed), "")
	if _, err := facades.Orm().Query().Exec(`UPDATE transactions SET created_at = created_at - interval '40 days' WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}
	for _, tx := range []models.Transaction{failed, old} {
		f.receipt(tx.TxHash, "0x0", 21_000)
	}
	f.receipt(succeeded.TxHash, "0x1", 21_000)
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	check := DepositReceiptCheck{ChainID: models.ChainTETH, Since: since}

	dry, err := f.svc.VerifyDepositReceipts(context.Background(), check)

	if err != nil {
		t.Fatal(err)
	}
	if dry.Checked != 2 || dry.Succeeded != 1 || len(dry.Failed) != 1 || dry.Marked != 0 || len(dry.Unverified) != 0 {
		t.Fatalf("dry run %+v, want 2 checked, 1 succeeded, 1 failed, none marked", dry)
	}
	if row := dry.Failed[0]; row.TransactionID != failed.ID || row.TxHash != failed.TxHash || row.FailureCode != outOfGasCode || row.Marked ||
		row.ToAddress != sepoliaDepositAddress || row.Amount != failedTransferBaseUnits {
		t.Fatalf("failed row %+v", row)
	}
	if got := reloadTransaction(t, failed.ID); got.Status != string(types.TxStatusConfirmed) || len(eventsOfType(t, types.EventDepositFailed)) != 0 {
		t.Fatal("a dry run writes nothing and sends nothing")
	}
	if receiptCallsFor(f.node, token.TxHash)+receiptCallsFor(f.node, old.TxHash) != 0 {
		t.Fatal("ERC-20 deposits and deposits recorded before --since are not read")
	}

	check.Apply = true
	applied, err := f.svc.VerifyDepositReceipts(context.Background(), check)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Marked != 1 || len(applied.Failed) != 1 || !applied.Failed[0].Marked {
		t.Fatalf("apply %+v", applied)
	}
	got := reloadTransaction(t, failed.ID)
	if got.Status != string(types.TxStatusFailed) || got.ErrorMessage != outOfGasCode || got.ConfirmedAt == nil {
		t.Fatalf("applied row: status %s error %q confirmed_at %v", got.Status, got.ErrorMessage, got.ConfirmedAt)
	}
	events := depositEventsFor(t, types.EventDepositFailed, failed.ID)
	if len(events) != 1 || events[0].Status != string(types.TxStatusFailed) || events[0].FailureCode == nil || events[0].ConfirmedAt == nil {
		t.Fatalf("deposit.failed of a deposit already confirmed %+v: it keeps confirmed_at so the receiver reverses its credit", events)
	}
	if got := reloadTransaction(t, succeeded.ID); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("succeeded deposit %s", got.Status)
	}

	again, err := f.svc.VerifyDepositReceipts(context.Background(), check)
	if err != nil {
		t.Fatal(err)
	}
	if again.Checked != 1 || len(again.Failed) != 0 || again.Marked != 0 || len(eventsOfType(t, types.EventDepositFailed)) != 1 {
		t.Fatalf("a second apply finds nothing new: %+v, %d deposit.failed events", again, len(eventsOfType(t, types.EventDepositFailed)))
	}

	everything, err := f.svc.VerifyDepositReceipts(context.Background(), DepositReceiptCheck{ChainID: models.ChainTETH})
	if err != nil {
		t.Fatal(err)
	}
	if len(everything.Failed) != 1 || everything.Failed[0].TransactionID != old.ID {
		t.Fatalf("without --since every confirmed native deposit is read: %+v", everything)
	}
}

func TestVerify_DepositReceipts_AReceiptThatCannotBeReadFailsTheRunAndKeepsTheDeposit(t *testing.T) {
	f := newFailedDepositFixture(t)
	failed := f.insertNativeDeposit(t, string(types.TxStatusConfirmed), "")
	unread := f.insertNativeDeposit(t, string(types.TxStatusConfirmed), "")
	f.receipt(failed.TxHash, "0x0", 21_000)

	report, err := f.svc.VerifyDepositReceipts(context.Background(), DepositReceiptCheck{ChainID: models.ChainTETH, Apply: true})

	if !errors.Is(err, ErrDepositReceiptsUnverified) {
		t.Fatalf("err %v, want ErrDepositReceiptsUnverified", err)
	}
	if len(report.Unverified) != 1 || report.Unverified[0].TransactionID != unread.ID || report.Unverified[0].Err == nil || report.Marked != 1 {
		t.Fatalf("report %+v", report)
	}
	if got := reloadTransaction(t, unread.ID); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("a deposit whose receipt was not read is left as it was, got %s", got.Status)
	}
	if got := reloadTransaction(t, failed.ID); got.Status != string(types.TxStatusFailed) {
		t.Fatalf("the other deposits are still verified, got %s", got.Status)
	}
}

func TestVerify_DepositReceipts_RefusesAChainWhoseAdapterReadsNoReceipts(t *testing.T) {
	svc, _ := setupDepositService(t)

	_, err := svc.VerifyDepositReceipts(context.Background(), DepositReceiptCheck{ChainID: "eth"})

	if !errors.Is(err, ErrReceiptsUnsupported) {
		t.Fatalf("err %v, want ErrReceiptsUnsupported", err)
	}
}
