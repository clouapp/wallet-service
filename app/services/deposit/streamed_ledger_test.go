package deposit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// The stream only names a ledger: the deposits are read from that ledger through
// the chain's own RPC adapter, like the scanner reads them, so a stream on another
// network can never be the source of a deposit.
func TestRecord_StreamedLedger_ReadsTheLedgerThroughTheChainAndConfirmsItsDepositsOnce(t *testing.T) {
	tag := xrpTaggedTag
	f := newXRPDepositFixture(t, xrpTransfer(xrpTaggedHash, xrpTaggedSender, xrpTaggedRecipient, xrpTaggedDrops, &tag))
	fixtures.InsertAddress(t, f.wallet.ID, models.ChainTXRP, xrpTaggedRecipient, "user_tagged", 1)

	recorded, err := f.svc.RecordStreamedLedger(context.Background(), models.ChainTXRP, xrpTaggedLedger)

	if err != nil || recorded != 1 || f.adapter.ScanBlockCalls != 1 {
		t.Fatalf("recorded %d err %v ledger reads %d, want the payment read from the ledger", recorded, err, f.adapter.ScanBlockCalls)
	}
	deposit := xrpDeposit(t, xrpTaggedHash)
	if deposit.Status != string(types.TxStatusConfirmed) || deposit.Confirmations != 1 || deposit.ConfirmedAt == nil ||
		deposit.DestinationTag == nil || *deposit.DestinationTag != xrpTaggedTag || deposit.BlockNumber != xrpTaggedLedger {
		t.Fatalf("deposit %+v, want it confirmed in its validated ledger with its tag", deposit)
	}
	xrpDepositEvent(t, types.EventDepositPending, deposit.ID)
	xrpDepositEvent(t, types.EventDepositConfirmed, deposit.ID)

	if again, err := f.svc.RecordStreamedLedger(context.Background(), models.ChainTXRP, xrpTaggedLedger); err != nil || again != 0 {
		t.Fatalf("second trigger recorded %d err %v", again, err)
	}
	if scanned, err := f.svc.ScanBlock(context.Background(), models.ChainTXRP, xrpTaggedLedger); err != nil || scanned != 0 {
		t.Fatalf("the scan of the same ledger recorded %d err %v", scanned, err)
	}
	xrpDepositEvent(t, types.EventDepositPending, deposit.ID)
	xrpDepositEvent(t, types.EventDepositConfirmed, deposit.ID)
}

func TestRecord_StreamedLedger_ConfirmsOnlyTheDepositsOfThatLedger(t *testing.T) {
	tag := xrpTaggedTag
	f := newXRPDepositFixture(t, xrpTransfer(xrpTaggedHash, xrpTaggedSender, xrpTaggedRecipient, xrpTaggedDrops, &tag))
	fixtures.InsertAddress(t, f.wallet.ID, models.ChainTXRP, xrpTaggedRecipient, "user_tagged", 1)
	older := fixtures.InsertTransaction(t, f.wallet.ID, nil, models.ChainTXRP, models.TxTypeDeposit, string(types.TxStatusPending), models.NativeXRP, "5", xrpTaggedLedger-10)

	if _, err := f.svc.RecordStreamedLedger(context.Background(), models.ChainTXRP, xrpTaggedLedger); err != nil {
		t.Fatal(err)
	}

	if got := reloadTransaction(t, older.ID); got.Status != string(types.TxStatusPending) {
		t.Fatalf("a deposit of ledger %d is %s: a streamed ledger confirms its own deposits only", older.BlockNumber, got.Status)
	}
	if got := xrpDeposit(t, xrpTaggedHash); got.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("the streamed ledger's deposit is %s", got.Status)
	}
}

func TestRecord_StreamedLedger_LeavesTheConfirmationToAScanThatHoldsTheChain(t *testing.T) {
	tag := xrpTaggedTag
	f := newXRPDepositFixture(t, xrpTransfer(xrpTaggedHash, xrpTaggedSender, xrpTaggedRecipient, xrpTaggedDrops, &tag))
	fixtures.InsertAddress(t, f.wallet.ID, models.ChainTXRP, xrpTaggedRecipient, "user_tagged", 1)
	held, err := cacheguard.Acquire(f.svc.cache, scanLockKey(models.ChainTXRP), time.Minute)
	if err != nil || !held {
		t.Fatalf("hold the scan lock: %v %v", held, err)
	}

	recorded, err := f.svc.RecordStreamedLedger(context.Background(), models.ChainTXRP, xrpTaggedLedger)

	if err != nil || recorded != 1 {
		t.Fatalf("recorded %d err %v: recording never waits for the scan lock", recorded, err)
	}
	if deposit := xrpDeposit(t, xrpTaggedHash); deposit.Status != string(types.TxStatusPending) {
		t.Fatalf("status %s, want pending: the scan holding the chain confirms it", deposit.Status)
	}
}

// lockProbe refreshes balances and reports whether the chain's scan lock was free
// while it did.
type lockProbe struct {
	svc      *Service
	chainID  string
	freeSeen []bool
}

func (p *lockProbe) RefreshWalletByID(context.Context, uuid.UUID) error {
	free, err := cacheguard.Acquire(p.svc.cache, scanLockKey(p.chainID), time.Minute)
	if err == nil && free {
		p.svc.cache.Forget(scanLockKey(p.chainID))
	}
	p.freeSeen = append(p.freeSeen, err == nil && free)
	return nil
}

func TestRecord_StreamedLedger_RefreshesBalancesAfterReleasingTheScanLock(t *testing.T) {
	tag := xrpTaggedTag
	f := newXRPDepositFixture(t, xrpTransfer(xrpTaggedHash, xrpTaggedSender, xrpTaggedRecipient, xrpTaggedDrops, &tag))
	fixtures.InsertAddress(t, f.wallet.ID, models.ChainTXRP, xrpTaggedRecipient, "user_tagged", 1)
	probe := &lockProbe{svc: f.svc, chainID: models.ChainTXRP}
	f.svc.SetBalanceRefresher(probe)

	if _, err := f.svc.RecordStreamedLedger(context.Background(), models.ChainTXRP, xrpTaggedLedger); err != nil {
		t.Fatal(err)
	}

	if len(probe.freeSeen) != 1 || !probe.freeSeen[0] {
		t.Fatalf("balance refreshes saw the scan lock free: %v, want one refresh after it was released", probe.freeSeen)
	}
}

func TestRecord_StreamedLedger_RefusesAnUnknownChainAndALedgerPastTheHead(t *testing.T) {
	f := newXRPDepositFixture(t)
	if _, err := f.svc.RecordStreamedLedger(context.Background(), "nochain", 1); err == nil {
		t.Fatal("an unknown chain was accepted")
	}
	if recorded, err := f.svc.RecordStreamedLedger(context.Background(), models.ChainTXRP, xrpTaggedLedger+1); err == nil || recorded != 0 {
		t.Fatalf("recorded %d err %v for a ledger the chain RPC has not validated yet", recorded, err)
	}
}
