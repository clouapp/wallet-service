package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// depositReceiver records the X-Vault-Event of every POST it accepts.
type depositReceiver struct {
	mu     sync.Mutex
	events []string
}

func (r *depositReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.events = append(r.events, req.Header.Get("X-Vault-Event"))
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (r *depositReceiver) received() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func TestDeliver_Superseded_ADepositProgressEventIsNeverSentOnceTheDepositFailed(t *testing.T) {
	svc, store := newMemoryWebhookService()
	receiver := &depositReceiver{}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg, configErr := svc.CreateConfig(context.Background(), server.URL, "superseded-secret", []string{
		string(types.EventDepositPending), string(types.EventDepositConfirmed), string(types.EventDepositFailed),
	}, nil)
	if configErr != nil {
		t.Fatal(configErr)
	}
	failedDeposit, otherDeposit := uuid.New(), uuid.New()
	row := func(eventType types.EventType, txID uuid.UUID) models.WebhookEvent {
		t.Helper()
		configID := cfg.ID
		event := &models.WebhookEvent{
			ID: uuid.New(), TransactionID: &txID, WebhookConfigID: &configID, EventType: string(eventType),
			Payload: `{"type":"` + string(eventType) + `"}`, DeliveryURL: server.URL,
			DeliveryStatus: models.WebhookDeliveryPending, MaxAttempts: 10,
		}
		if err := store.addEvent(event); err != nil {
			t.Fatal(err)
		}
		return *event
	}
	staleConfirmed := row(types.EventDepositConfirmed, failedDeposit)
	stalePending := row(types.EventDepositPending, failedDeposit)
	failed := row(types.EventDepositFailed, failedDeposit)
	unrelated := row(types.EventDepositConfirmed, otherDeposit)

	// A message still in the queue for the stale deposit.confirmed reaches the worker.
	if err := svc.Deliver(context.Background(), queuedMessage(staleConfirmed, 2)); err != nil {
		t.Fatalf("a superseded event is settled, not retried: %v", err)
	}
	delivered, err := svc.DeliverPending(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}

	got := receiver.received()
	if delivered != 2 || len(got) != 2 {
		t.Fatalf("delivered %d, received %v: want deposit.failed and the other deposit's deposit.confirmed only", delivered, got)
	}
	for _, eventType := range got {
		if eventType != string(types.EventDepositFailed) && eventType != string(types.EventDepositConfirmed) {
			t.Fatalf("received %v", got)
		}
	}
	byID := map[uuid.UUID]models.WebhookEvent{}
	for _, event := range store.listEvents() {
		byID[event.ID] = event
	}
	for _, stale := range []models.WebhookEvent{staleConfirmed, stalePending} {
		if got := byID[stale.ID]; got.DeliveryStatus != models.WebhookDeliveryFailed || got.LastError != errSupersededByDepositFailure {
			t.Fatalf("%s of the failed deposit: status %s error %q, want failed and superseded", stale.EventType, got.DeliveryStatus, got.LastError)
		}
	}
	for _, sent := range []models.WebhookEvent{failed, unrelated} {
		if got := byID[sent.ID]; got.DeliveryStatus != models.WebhookDeliveryDelivered {
			t.Fatalf("%s: status %s, want delivered", sent.EventType, got.DeliveryStatus)
		}
	}

	if err := svc.Deliver(context.Background(), queuedMessage(stalePending, 3)); err != nil {
		t.Fatal(err)
	}
	if len(receiver.received()) != 2 {
		t.Fatal("a superseded event asked for again (a redelivery or a redispatch) is still not sent")
	}
}
