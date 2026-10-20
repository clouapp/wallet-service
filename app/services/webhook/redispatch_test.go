package webhook

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const redispatchBatch = 10

// queueFixture is a webhook service whose queue sender the test controls: every
// send fails while down is set, and a send of an event listed in fail fails.
type queueFixture struct {
	svc   *Service
	store *memoryWebhook
	sent  []types.WebhookMessage
	down  bool
	fail  map[string]bool
}

func newQueueFixture(t *testing.T) *queueFixture {
	t.Helper()
	f := &queueFixture{store: newMemoryWebhook(), fail: map[string]bool{}}
	sender := mocks.NewMockSender(t)
	sender.EXPECT().SendWebhook(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, msg types.WebhookMessage) error {
		if f.down || f.fail[msg.EventID] {
			return errors.New("sqs unavailable")
		}
		f.sent = append(f.sent, msg)
		return nil
	}).Maybe()
	f.svc = NewService(Deps{SQS: sender, Configs: f.store, Events: memoryEvents{store: f.store}, Destinations: testDestinations()})
	return f
}

// lostEvent enqueues one deposit.confirmed while the queue is down, as an SQS
// outage at insert time does, and ages it past the re-dispatch threshold.
func (f *queueFixture) lostEvent(t *testing.T, accountID uuid.UUID) string {
	t.Helper()
	walletID := uuid.New()
	before := len(f.store.listEvents())
	f.down = true
	_, err := f.svc.EnqueueScoped(context.Background(), ScopedEvent{
		EventType: types.EventDepositConfirmed,
		SubjectID: uuid.NewString(),
		WalletID:  walletID,
		AccountID: &accountID,
		Data:      map[string]string{"wallet_id": walletID.String()},
	})
	f.down = false
	if err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	events := f.store.listEvents()
	if len(events) != before+1 {
		t.Fatalf("expected one stored event, got %d", len(events)-before)
	}
	id := events[len(events)-1].ID.String()
	f.store.backdate(id, RedispatchStaleAfter+time.Minute)
	return id
}

func TestRedispatch_Stale_SendsAnEventWhoseQueueMessageWasLost(t *testing.T) {
	f := newQueueFixture(t)
	accountID := uuid.New()
	cfg, err := f.svc.CreateConfig(context.Background(), "https://owner.test/hook", "secret", []string{string(types.EventDepositConfirmed)}, &accountID)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	eventID := f.lostEvent(t, accountID)
	if len(f.sent) != 0 {
		t.Fatalf("the insert-time send must have failed, sent %d", len(f.sent))
	}

	sent, err := f.svc.RedispatchStale(context.Background(), redispatchBatch)
	if err != nil {
		t.Fatalf("RedispatchStale: %v", err)
	}
	if sent != 1 || len(f.sent) != 1 {
		t.Fatalf("sent = %d (queue got %d), want 1", sent, len(f.sent))
	}
	msg := f.sent[0]
	stored := f.store.listEvents()[0]
	if msg.EventID != eventID || msg.ConfigID != cfg.ID.String() || msg.EventType != types.EventDepositConfirmed {
		t.Fatalf("message = %+v", msg)
	}
	if msg.Payload != stored.Payload || msg.DeliveryURL != cfg.URL {
		t.Fatal("the re-dispatched message must carry the stored payload and URL")
	}
	if stored.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1: a re-dispatch counts as one attempt", stored.Attempts)
	}

	again, err := f.svc.RedispatchStale(context.Background(), redispatchBatch)
	if err != nil || again != 0 {
		t.Fatalf("second run sent %d (err %v); an event just re-dispatched must wait", again, err)
	}
}

func TestRedispatch_Stale_LeavesAFreshEventToTheQueue(t *testing.T) {
	f := newQueueFixture(t)
	accountID := uuid.New()
	if _, err := f.svc.CreateConfig(context.Background(), "https://owner.test/hook", "secret", []string{string(types.EventDepositConfirmed)}, &accountID); err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	if _, err := f.svc.EnqueueScoped(context.Background(), ScopedEvent{
		EventType: types.EventDepositConfirmed,
		SubjectID: uuid.NewString(),
		WalletID:  uuid.New(),
		AccountID: &accountID,
		Data:      map[string]string{"test": "data"},
	}); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	queued := len(f.sent)

	sent, err := f.svc.RedispatchStale(context.Background(), redispatchBatch)
	if err != nil {
		t.Fatalf("RedispatchStale: %v", err)
	}
	if sent != 0 || len(f.sent) != queued {
		t.Fatalf("an event still in flight was sent again (%d)", sent)
	}
}

func TestRedispatch_Stale_KeepsGoingAfterASendFailure(t *testing.T) {
	f := newQueueFixture(t)
	accountID := uuid.New()
	if _, err := f.svc.CreateConfig(context.Background(), "https://owner.test/hook", "secret", []string{string(types.EventDepositConfirmed)}, &accountID); err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	first := f.lostEvent(t, accountID)
	second := f.lostEvent(t, accountID)
	f.fail[first] = true

	sent, err := f.svc.RedispatchStale(context.Background(), redispatchBatch)
	if err != nil {
		t.Fatalf("RedispatchStale: %v", err)
	}
	if sent != 1 || len(f.sent) != 1 || f.sent[0].EventID != second {
		t.Fatalf("sent = %d %+v, want only %s", sent, f.sent, second)
	}
}

func TestRedispatch_Stale_RefusesWithoutAQueueOrALimit(t *testing.T) {
	svc, _ := newMemoryWebhookService()
	if _, err := svc.RedispatchStale(context.Background(), redispatchBatch); err == nil {
		t.Fatal("a service without a queue sender must refuse to re-dispatch")
	}
	f := newQueueFixture(t)
	if _, err := f.svc.RedispatchStale(context.Background(), 0); err == nil {
		t.Fatal("a non-positive limit must be refused")
	}
}
