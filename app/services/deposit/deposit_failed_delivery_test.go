package deposit

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
)

// staleDepositEvent publishes eventType for tx and returns its outbox row, left
// undelivered as if the receiver had been down.
func (f failedDepositFixture) staleDepositEvent(t *testing.T, eventType types.EventType, tx models.Transaction) models.WebhookEvent {
	t.Helper()
	if err := f.publisher.Publish(context.Background(), eventType, tx); err != nil {
		t.Fatal(err)
	}
	subject := tx.ID.String()
	if eventType == types.EventDepositConfirming {
		subject = depositConfirmingSubject(tx)
	}
	var row models.WebhookEvent
	for _, event := range eventsOfType(t, eventType) {
		if event.SubjectID != nil && *event.SubjectID == subject {
			row = event
		}
	}
	if row.ID == uuid.Nil {
		t.Fatalf("no %s row for %s", eventType, subject)
	}
	return row
}

func depositConfirmingSubject(tx models.Transaction) string {
	return tx.ID.String() + ":" + strconv.Itoa(tx.Confirmations)
}

// queued is the message the worker receives for row, as the queue carries it.
func (f failedDepositFixture) queued(row models.WebhookEvent) types.WebhookMessage {
	return types.WebhookMessage{
		EventID: row.ID.String(), EventType: types.EventType(row.EventType), Payload: row.Payload,
		DeliveryURL: row.DeliveryURL, ConfigID: f.config.ID.String(), TransactionID: row.TransactionID.String(), Attempt: row.Attempts + 1,
	}
}

func (f failedDepositFixture) deliverOutbox(t *testing.T) {
	t.Helper()
	if _, err := f.webhooks.DeliverPending(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
}

func TestDeposit_Failed_NoEarlierEventOfTheDepositIsDeliveredAfterIt(t *testing.T) {
	f := newFailedDepositFixture(t)
	failed := f.insertNativeDeposit(t, string(types.TxStatusConfirming), "")
	f.receipt(failed.TxHash, "0x0", 21_000)
	stalePending := f.staleDepositEvent(t, types.EventDepositPending, failed)
	staleConfirming := f.staleDepositEvent(t, types.EventDepositConfirming, failed)

	if err := f.svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloadTransaction(t, failed.ID); got.Status != string(types.TxStatusFailed) {
		t.Fatalf("status %s, want failed", got.Status)
	}

	// The queue still holds a message of the stale deposit.confirming.
	if err := f.webhooks.Deliver(context.Background(), f.queued(staleConfirming)); err != nil {
		t.Fatalf("a superseded message is settled so the queue drops it: %v", err)
	}
	f.deliverOutbox(t)
	if got := f.receiver.received(); len(got) != 1 || got[0] != string(types.EventDepositFailed) {
		t.Fatalf("the receiver got %v, want deposit.failed alone", got)
	}

	// A redelivery the client asks for does not send them either.
	requeued, err := f.webhooks.Redeliver(context.Background(), f.accountID, f.config.ID, webhook.RedeliverySelection{EventIDs: []uuid.UUID{stalePending.ID, staleConfirming.ID}})
	if err != nil || requeued != 2 {
		t.Fatalf("requeued %d err %v, want both stale events back in the outbox", requeued, err)
	}
	f.deliverOutbox(t)
	if got := f.receiver.received(); len(got) != 1 {
		t.Fatalf("after redelivering %d superseded events the receiver got %v, want deposit.failed alone", requeued, got)
	}
}

func TestDeposit_Failed_AConfirmedDepositWhoseConfirmationIsStillUndeliveredSendsOnlyTheFailure(t *testing.T) {
	f := newFailedDepositFixture(t)
	confirmed := f.insertNativeDeposit(t, string(types.TxStatusConfirmed), "")
	f.receipt(confirmed.TxHash, "0x0", 21_000)
	staleConfirmed := f.staleDepositEvent(t, types.EventDepositConfirmed, confirmed)

	if _, err := f.svc.VerifyDepositReceipts(context.Background(), DepositReceiptCheck{ChainID: models.ChainTETH, Apply: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.webhooks.Deliver(context.Background(), f.queued(staleConfirmed)); err != nil {
		t.Fatal(err)
	}
	f.deliverOutbox(t)

	if got := f.receiver.received(); len(got) != 1 || got[0] != string(types.EventDepositFailed) {
		t.Fatalf("the receiver got %v: a deposit.confirmed still in retry would credit a deposit that failed", got)
	}
	rows := webhookEvents(t, string(types.EventDepositConfirmed), confirmed.ID)
	if len(rows) != 1 || rows[0].DeliveryStatus != models.WebhookDeliveryFailed {
		t.Fatalf("stale deposit.confirmed rows %+v, want one settled as failed", rows)
	}
}
