package deposit

import (
	"context"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func TestRecord_ValidatedTransfers_RecordsAndConfirmsAStreamedPaymentOnce(t *testing.T) {
	tag := xrpTaggedTag
	payment := xrpTransfer(xrpTaggedHash, xrpTaggedSender, xrpTaggedRecipient, xrpTaggedDrops, &tag)
	f := newXRPDepositFixture(t, payment)
	fixtures.InsertAddress(t, f.wallet.ID, models.ChainTXRP, xrpTaggedRecipient, "user_tagged", 1)
	unwatched := xrpTransfer(xrpUntaggedHash, xrpUntaggedSender, xrpUntaggedRecipient, xrpUntaggedDrops, nil)

	recorded, err := f.svc.RecordValidatedTransfers(context.Background(), models.ChainTXRP, xrpTaggedLedger, []types.DetectedTransfer{payment, unwatched})

	if err != nil || recorded != 1 {
		t.Fatalf("recorded %d err %v, want the payment to the watched address", recorded, err)
	}
	deposit := xrpDeposit(t, xrpTaggedHash)
	if deposit.Status != string(types.TxStatusConfirmed) || deposit.Confirmations != 1 || deposit.ConfirmedAt == nil ||
		deposit.DestinationTag == nil || *deposit.DestinationTag != xrpTaggedTag || deposit.BlockNumber != xrpTaggedLedger {
		t.Fatalf("deposit %+v, want it confirmed in its validated ledger with its tag", deposit)
	}
	xrpDepositEvent(t, types.EventDepositPending, deposit.ID)
	xrpDepositEvent(t, types.EventDepositConfirmed, deposit.ID)

	// The ledger scan and a second stream delivery of the same payment record nothing.
	if again, err := f.svc.RecordValidatedTransfers(context.Background(), models.ChainTXRP, xrpTaggedLedger+1, []types.DetectedTransfer{payment}); err != nil || again != 0 {
		t.Fatalf("second delivery recorded %d err %v", again, err)
	}
	if scanned, err := f.svc.ScanBlock(context.Background(), models.ChainTXRP, xrpTaggedLedger); err != nil || scanned != 0 {
		t.Fatalf("the scan of the same ledger recorded %d err %v", scanned, err)
	}
	xrpDepositEvent(t, types.EventDepositPending, deposit.ID)
	xrpDepositEvent(t, types.EventDepositConfirmed, deposit.ID)
}

func TestRecord_ValidatedTransfers_LeavesTheConfirmationToAScanThatHoldsTheChain(t *testing.T) {
	tag := xrpTaggedTag
	payment := xrpTransfer(xrpTaggedHash, xrpTaggedSender, xrpTaggedRecipient, xrpTaggedDrops, &tag)
	f := newXRPDepositFixture(t, payment)
	fixtures.InsertAddress(t, f.wallet.ID, models.ChainTXRP, xrpTaggedRecipient, "user_tagged", 1)
	held, err := cacheguard.Acquire(f.svc.cache, scanLockKey(models.ChainTXRP), time.Minute)
	if err != nil || !held {
		t.Fatalf("hold the scan lock: %v %v", held, err)
	}

	recorded, err := f.svc.RecordValidatedTransfers(context.Background(), models.ChainTXRP, xrpTaggedLedger, []types.DetectedTransfer{payment})

	if err != nil || recorded != 1 {
		t.Fatalf("recorded %d err %v: recording never waits for the scan lock", recorded, err)
	}
	if deposit := xrpDeposit(t, xrpTaggedHash); deposit.Status != string(types.TxStatusPending) {
		t.Fatalf("status %s, want pending: the scan holding the chain confirms it", deposit.Status)
	}
	f.svc.cache.Forget(scanLockKey(models.ChainTXRP))
	if err := f.svc.updateConfirmations(context.Background(), models.ChainTXRP, f.adapter, xrpTaggedLedger); err != nil {
		t.Fatal(err)
	}
	if deposit := xrpDeposit(t, xrpTaggedHash); deposit.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("status %s after the scan's confirmation step", deposit.Status)
	}
}

func TestRecord_ValidatedTransfers_AnUnknownChainIsAnError(t *testing.T) {
	f := newXRPDepositFixture(t)
	if _, err := f.svc.RecordValidatedTransfers(context.Background(), "nochain", 1, nil); err == nil {
		t.Fatal("an unknown chain was accepted")
	}
}
