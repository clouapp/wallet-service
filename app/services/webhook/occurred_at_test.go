package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// insertConfigCreated inserts an owned config whose created_at is createdAt.
func (f scopedFixture) insertConfigCreated(t *testing.T, url string, createdAt time.Time) models.WebhookConfig {
	t.Helper()
	cfg := &models.WebhookConfig{
		ID:        uuid.New(),
		URL:       url,
		Secret:    scopedSecret,
		Events:    pgArray([]string{withdrawalEvents}),
		IsActive:  true,
		AccountID: &f.accountID,
	}
	cfg.CreatedAt = carbon.NewDateTime(carbon.FromStdTime(createdAt))
	if err := f.store.Create(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return *cfg
}

func TestEnqueue_Scoped_AnEventThatOccurredBeforeAConfigExistedSkipsIt(t *testing.T) {
	f := newScopedFixture(t)
	occurredAt := time.Now().UTC().Add(-time.Hour)
	older := f.insertConfigCreated(t, "https://older.test/hook", occurredAt.Add(-time.Minute))
	sameInstant := f.insertConfigCreated(t, "https://same.test/hook", occurredAt)
	newer := f.insertConfigCreated(t, "https://newer.test/hook", occurredAt.Add(time.Minute))
	event := f.event(uuid.NewString())
	event.OccurredAt = occurredAt

	enqueued, err := f.svc.EnqueueScoped(context.Background(), event)
	if err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, stored := range f.storedEvents() {
		got[*stored.WebhookConfigID] = true
	}
	if enqueued != 2 || !got[older.ID] || !got[sameInstant.ID] {
		t.Fatalf("enqueued = %d %v; the configs that existed when the event occurred must receive it", enqueued, got)
	}
	if got[newer.ID] {
		t.Fatal("a config created after the event occurred must not receive it")
	}
}

func TestEnqueue_Scoped_AnEventWithoutOccurredAtReachesEveryConfig(t *testing.T) {
	f := newScopedFixture(t)
	f.insertConfigCreated(t, "https://older.test/hook", time.Now().UTC().Add(-time.Hour))
	f.insertConfigCreated(t, "https://newer.test/hook", time.Now().UTC().Add(time.Hour))
	f.insertConfig(t, "https://unknown-creation.test/hook", []string{withdrawalEvents}, &f.accountID, nil)

	enqueued, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString()))
	if err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if enqueued != 3 {
		t.Fatalf("enqueued = %d, want 3: a live event is not filtered by creation time", enqueued)
	}
}
