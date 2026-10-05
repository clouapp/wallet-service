package webhook

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestDeliverPending_StoredLimitAndTimeoutOverrideTheEventRow(t *testing.T) {
	f := newScopedFixture(t)
	var hit atomic.Bool
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit.Store(true)
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	insertOwnedConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	if _, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString())); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if stored := storedEvents(t)[0]; stored.MaxAttempts != defaultMaxAttempts {
		t.Fatalf("enqueued max attempts = %d, want the default %d", stored.MaxAttempts, defaultMaxAttempts)
	}

	f.svc.SetDeliverySettingsSource(func(context.Context) (DeliverySettings, error) {
		return DeliverySettings{MaxAttempts: 1, Timeout: 150 * time.Millisecond}, nil
	})
	delivered, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize)
	if err != nil || delivered != 0 {
		t.Fatalf("DeliverPending = %d, %v", delivered, err)
	}
	if !hit.Load() {
		t.Fatal("delivery did not call the receiver")
	}
	stored := storedEvents(t)[0]
	if stored.DeliveryStatus != models.WebhookDeliveryFailed || stored.Attempts != 1 {
		t.Fatalf("after the settings limit: attempts=%d status=%s max_column=%d", stored.Attempts, stored.DeliveryStatus, stored.MaxAttempts)
	}
	if stored.MaxAttempts != defaultMaxAttempts {
		t.Fatalf("delivery rewrote the enqueued max attempts column to %d", stored.MaxAttempts)
	}
}

func TestDeliverPending_SettingsOutageKeepsTheDefaultAndStillDelivers(t *testing.T) {
	f := newScopedFixture(t)
	f.svc.SetDeliverySettingsSource(func(context.Context) (DeliverySettings, error) {
		return DeliverySettings{}, errors.New("settings down")
	})
	receiver := &recordingReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	insertOwnedConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	if _, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString())); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if stored := storedEvents(t)[0]; stored.MaxAttempts != defaultMaxAttempts {
		t.Fatalf("outage stamped max attempts %d", stored.MaxAttempts)
	}
	delivered, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize)
	if err != nil || delivered != 1 || len(receiver.bodies) != 1 {
		t.Fatalf("DeliverPending = %d, %v, bodies %d", delivered, err, len(receiver.bodies))
	}
	if stored := storedEvents(t)[0]; stored.DeliveryStatus != models.WebhookDeliveryDelivered {
		t.Fatalf("status = %s", stored.DeliveryStatus)
	}
}

func TestDeliverySettingsFromStored_ZeroKeepsTheDefault(t *testing.T) {
	t.Parallel()

	got := DeliverySettingsFromStored(0, 0)
	want := DefaultDeliverySettings()
	if got != want {
		t.Fatalf("from stored zeros = %+v, want %+v", got, want)
	}
	got = DeliverySettingsFromStored(3, 2)
	if got.MaxAttempts != 3 || got.Timeout != 2*time.Second {
		t.Fatalf("from stored = %+v", got)
	}
}

func TestResolveDeliverySettings_NilSourceKeepsTheDefault(t *testing.T) {
	t.Parallel()

	got := NewService(Deps{}).resolveDeliverySettings(context.Background())
	if got != DefaultDeliverySettings() {
		t.Fatalf("resolved = %+v", got)
	}
}
