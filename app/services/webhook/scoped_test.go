package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	scopedSecret      = "scoped-test-secret"
	withdrawalEvents  = "withdrawal.broadcast"
	deliveryBatchSize = 10
)

type scopedFixture struct {
	svc       *Service
	store     *memoryWebhook
	accountID uuid.UUID
	wallet    models.Wallet
}

func newScopedFixture(t *testing.T) scopedFixture {
	t.Helper()
	svc, store := newMemoryWebhookService()
	return scopedFixture{
		svc:       svc,
		store:     store,
		accountID: uuid.New(),
		wallet:    models.Wallet{ID: uuid.New(), Chain: "eth"},
	}
}

func (f scopedFixture) event(subjectID string) ScopedEvent {
	accountID := f.accountID
	return ScopedEvent{
		EventType: types.EventWithdrawalBroadcast,
		SubjectID: subjectID,
		WalletID:  f.wallet.ID,
		AccountID: &accountID,
		Data:      map[string]string{"withdrawal_id": subjectID},
	}
}

func (f scopedFixture) insertConfig(t *testing.T, url string, events []string, accountID, walletID *uuid.UUID) models.WebhookConfig {
	t.Helper()
	cfg := &models.WebhookConfig{
		ID:        uuid.New(),
		URL:       url,
		Secret:    scopedSecret,
		Events:    pgArray(events),
		IsActive:  true,
		AccountID: accountID,
		WalletID:  walletID,
	}
	if err := f.store.Create(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return *cfg
}

func (f scopedFixture) storedEvents() []models.WebhookEvent {
	return f.store.listEvents()
}

func TestEnqueue_Scoped_DeliversOnlyToConfigsThatCanSeeTheWallet(t *testing.T) {
	f := newScopedFixture(t)
	otherAccountID := uuid.New()
	otherWallet := models.Wallet{ID: uuid.New(), Chain: "eth"}

	legacy := f.insertConfig(t, "https://legacy.test/hook", []string{withdrawalEvents}, nil, nil)
	owned := f.insertConfig(t, "https://owned.test/hook", []string{withdrawalEvents}, &f.accountID, nil)
	walletScoped := f.insertConfig(t, "https://wallet.test/hook", []string{withdrawalEvents}, nil, &f.wallet.ID)
	f.insertConfig(t, "https://other-account.test/hook", []string{withdrawalEvents}, &otherAccountID, nil)
	f.insertConfig(t, "https://other-wallet.test/hook", []string{withdrawalEvents}, nil, &otherWallet.ID)
	f.insertConfig(t, "https://deposits-only.test/hook", []string{"deposit.confirmed"}, &f.accountID, nil)

	enqueued, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString()))
	if err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if enqueued != 3 {
		t.Fatalf("enqueued = %d, want 3 (legacy, owned, wallet-scoped)", enqueued)
	}

	got := map[uuid.UUID]bool{}
	for _, event := range f.storedEvents() {
		if event.WebhookConfigID == nil || event.SubjectID == nil {
			t.Fatalf("event %s must record its config and subject", event.ID)
		}
		got[*event.WebhookConfigID] = true
	}
	for _, cfg := range []models.WebhookConfig{legacy, owned, walletScoped} {
		if !got[cfg.ID] {
			t.Errorf("config %s (%s) did not receive the event", cfg.ID, cfg.URL)
		}
	}
}

func TestEnqueue_Scoped_SameSubjectIsEnqueuedOncePerConfig(t *testing.T) {
	f := newScopedFixture(t)
	f.insertConfig(t, "https://owned.test/hook", []string{withdrawalEvents}, &f.accountID, nil)
	subject := uuid.NewString()

	first, err := f.svc.EnqueueScoped(context.Background(), f.event(subject))
	if err != nil || first != 1 {
		t.Fatalf("first enqueue = %d, %v", first, err)
	}
	second, err := f.svc.EnqueueScoped(context.Background(), f.event(subject))
	if err != nil || second != 0 {
		t.Fatalf("second enqueue = %d, %v; want 0 (deduplicated)", second, err)
	}
	if n := len(f.storedEvents()); n != 1 {
		t.Fatalf("stored events = %d, want 1", n)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(f.storedEvents()[0].Payload), &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["id"] != f.storedEvents()[0].ID.String() || payload["type"] != withdrawalEvents {
		t.Fatalf("payload envelope = %+v", payload)
	}
}

func TestEnqueue_Scoped_RejectsIncompleteEvents(t *testing.T) {
	f := newScopedFixture(t)
	event := f.event("")
	if _, err := f.svc.EnqueueScoped(context.Background(), event); err == nil {
		t.Fatal("an event without subject must be rejected")
	}
	event = f.event(uuid.NewString())
	event.WalletID = uuid.Nil
	if _, err := f.svc.EnqueueScoped(context.Background(), event); err == nil {
		t.Fatal("an event without wallet must be rejected")
	}
}

type recordingReceiver struct {
	mu         sync.Mutex
	status     int
	signatures []string
	bodies     []string
	eventTypes []string
}

func (r *recordingReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, string(body))
	r.signatures = append(r.signatures, req.Header.Get("X-Vault-Signature"))
	r.eventTypes = append(r.eventTypes, req.Header.Get("X-Vault-Event"))
	w.WriteHeader(r.status)
}

func TestDeliver_Pending_SignsAndMarksDelivered(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	f.insertConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	if _, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString())); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	delivered, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize)
	if err != nil || delivered != 1 {
		t.Fatalf("DeliverPending = %d, %v", delivered, err)
	}

	mac := hmac.New(sha256.New, []byte(scopedSecret))
	mac.Write([]byte(receiver.bodies[0]))
	if receiver.signatures[0] != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("X-Vault-Signature must be the HMAC-SHA256 of the raw body")
	}
	if receiver.eventTypes[0] != withdrawalEvents {
		t.Fatalf("X-Vault-Event = %s", receiver.eventTypes[0])
	}
	if stored := f.storedEvents()[0]; stored.DeliveryStatus != models.WebhookDeliveryDelivered {
		t.Fatalf("delivery status = %s", stored.DeliveryStatus)
	}

	again, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize)
	if err != nil || again != 0 || len(receiver.bodies) != 1 {
		t.Fatalf("a delivered event must not be sent again: %d, %v, %d", again, err, len(receiver.bodies))
	}
}

func TestDeliver_Pending_BacksOffAfterAFailureAndGivesUpAtMaxAttempts(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusServiceUnavailable}
	server := httptest.NewServer(receiver)
	defer server.Close()
	f.insertConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	if _, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString())); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if delivered, _ := f.svc.DeliverPending(context.Background(), deliveryBatchSize); delivered != 0 {
		t.Fatalf("a 503 must not count as delivered")
	}
	if _, _ = f.svc.DeliverPending(context.Background(), deliveryBatchSize); len(receiver.bodies) != 1 {
		t.Fatalf("the retry must wait for the backoff, got %d sends", len(receiver.bodies))
	}

	stored := f.storedEvents()[0]
	if stored.Attempts != 1 || stored.DeliveryStatus != models.WebhookDeliveryPending || stored.LastError == "" {
		t.Fatalf("after one failure: attempts=%d status=%s error=%q", stored.Attempts, stored.DeliveryStatus, stored.LastError)
	}

	f.store.age(stored.ID, stored.MaxAttempts-1, time.Now().UTC().Add(-24*time.Hour))
	if _, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize); err != nil {
		t.Fatalf("DeliverPending: %v", err)
	}
	stored = f.storedEvents()[0]
	if stored.DeliveryStatus != models.WebhookDeliveryFailed || stored.Attempts != stored.MaxAttempts {
		t.Fatalf("after the last attempt: attempts=%d/%d status=%s", stored.Attempts, stored.MaxAttempts, stored.DeliveryStatus)
	}
}

func TestDeliver_Pending_FailsEventsOfInactiveConfigs(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg := f.insertConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	if _, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString())); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	f.store.setActive(cfg.ID, false)
	if _, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize); err != nil {
		t.Fatalf("DeliverPending: %v", err)
	}
	if len(receiver.bodies) != 0 || f.storedEvents()[0].DeliveryStatus != models.WebhookDeliveryFailed {
		t.Fatal("events of an inactive config must fail without being sent")
	}
}

func TestUpdate_AccountConfig_OwnershipAndClaim(t *testing.T) {
	f := newScopedFixture(t)
	ctx := context.Background()
	otherAccountID := uuid.New()
	newEvents := []string{"deposit.confirmed", "withdrawal.broadcast", "withdrawal.confirmed", "withdrawal.failed"}

	owned := f.insertConfig(t, "https://owned.test/hook", []string{"deposit.confirmed"}, &f.accountID, nil)
	updated, err := f.svc.UpdateAccountConfig(ctx, f.accountID, owned.ID, ConfigUpdate{Events: newEvents})
	if err != nil {
		t.Fatalf("update owned config: %v", err)
	}
	for _, event := range newEvents {
		if !containsEvent(updated.Events, event) {
			t.Errorf("updated events %s miss %s", updated.Events, event)
		}
	}

	if _, err := f.svc.UpdateAccountConfig(ctx, otherAccountID, owned.ID, ConfigUpdate{Events: newEvents}); !errors.Is(err, ErrWebhookConfigNotFound) {
		t.Fatalf("another account must not see the config, got %v", err)
	}

	legacy := f.insertConfig(t, "https://legacy.test/hook", []string{"deposit.confirmed"}, nil, nil)
	if _, err := f.svc.UpdateAccountConfig(ctx, f.accountID, legacy.ID, ConfigUpdate{Events: newEvents, Secret: "wrong"}); !errors.Is(err, ErrWebhookOwnershipNotProven) {
		t.Fatalf("a legacy config needs its secret, got %v", err)
	}
	claimed, err := f.svc.UpdateAccountConfig(ctx, f.accountID, legacy.ID, ConfigUpdate{Events: newEvents, Secret: scopedSecret})
	if err != nil {
		t.Fatalf("claim legacy config: %v", err)
	}
	if claimed.AccountID == nil || *claimed.AccountID != f.accountID {
		t.Fatalf("legacy config must now belong to the account, got %v", claimed.AccountID)
	}
	if _, err := f.svc.UpdateAccountConfig(ctx, otherAccountID, legacy.ID, ConfigUpdate{Events: newEvents, Secret: scopedSecret}); !errors.Is(err, ErrWebhookConfigNotFound) {
		t.Fatalf("a claimed config is no longer claimable, got %v", err)
	}

	walletConfig := f.insertConfig(t, "https://wallet.test/hook", []string{"deposit.confirmed"}, nil, &f.wallet.ID)
	if _, err := f.svc.UpdateAccountConfig(ctx, f.accountID, walletConfig.ID, ConfigUpdate{Events: newEvents, Secret: scopedSecret}); !errors.Is(err, ErrWebhookConfigNotFound) {
		t.Fatalf("wallet configs are managed by the wallet endpoints, got %v", err)
	}
}

func TestUpdate_AccountConfig_ValidatesTheUpdate(t *testing.T) {
	f := newScopedFixture(t)
	ctx := context.Background()
	owned := f.insertConfig(t, "https://owned.test/hook", []string{"deposit.confirmed"}, &f.accountID, nil)

	if _, err := f.svc.UpdateAccountConfig(ctx, f.accountID, owned.ID, ConfigUpdate{}); !errors.Is(err, ErrWebhookUpdateEmpty) {
		t.Fatalf("empty update: %v", err)
	}
	if _, err := f.svc.UpdateAccountConfig(ctx, f.accountID, owned.ID, ConfigUpdate{Events: []string{}}); !errors.Is(err, ErrWebhookEventsEmpty) {
		t.Fatalf("empty events: %v", err)
	}
	if _, err := f.svc.UpdateAccountConfig(ctx, f.accountID, owned.ID, ConfigUpdate{Events: []string{"withdrawal.completed"}}); !errors.Is(err, ErrWebhookUnknownEvent) {
		t.Fatalf("unknown event: %v", err)
	}

	inactive := false
	updated, err := f.svc.UpdateAccountConfig(ctx, f.accountID, owned.ID, ConfigUpdate{IsActive: &inactive})
	if err != nil || updated.IsActive {
		t.Fatalf("deactivate: %v / %v", updated, err)
	}
	visible, err := f.svc.ListAccountConfigs(ctx, f.accountID)
	if err != nil || len(visible) != 1 || visible[0].IsActive {
		t.Fatalf("list after deactivate = %+v, %v", visible, err)
	}
}

func TestEnqueue_Event_LegacyPathNeverReachesAccountOwnedConfigs(t *testing.T) {
	f := newScopedFixture(t)
	otherWallet := models.Wallet{ID: uuid.New(), Chain: "eth"}
	const sweepEvent = "sweep.confirmed"

	legacy := f.insertConfig(t, "https://legacy.test/hook", []string{sweepEvent}, nil, nil)
	walletScoped := f.insertConfig(t, "https://wallet.test/hook", []string{sweepEvent}, nil, &f.wallet.ID)
	f.insertConfig(t, "https://owned.test/hook", []string{sweepEvent}, &f.accountID, nil)
	f.insertConfig(t, "https://other-wallet.test/hook", []string{sweepEvent}, nil, &otherWallet.ID)

	tx := models.Transaction{ID: uuid.New(), WalletID: f.wallet.ID, Chain: "eth", TxType: models.TxTypeSweep, Status: "confirmed", Asset: "ETH", Amount: "1000", TxHash: "sweep-tx"}
	f.svc.EnqueueEvent(context.Background(), tx.ID, types.EventType(sweepEvent), tx)

	got := map[uuid.UUID]bool{}
	for _, event := range f.storedEvents() {
		got[*event.WebhookConfigID] = true
	}
	if len(got) != 2 || !got[legacy.ID] || !got[walletScoped.ID] {
		t.Fatalf("legacy event reached %v, want only the ownerless and matching-wallet configs", got)
	}
}

func TestEnqueue_Event_WalletConfigsSkipEventsWithoutAKnownWallet(t *testing.T) {
	f := newScopedFixture(t)
	const sweepEvent = "sweep.confirmed"

	legacy := f.insertConfig(t, "https://legacy.test/hook", []string{sweepEvent}, nil, nil)
	f.insertConfig(t, "https://wallet.test/hook", []string{sweepEvent}, nil, &f.wallet.ID)

	tx := models.Transaction{ID: uuid.New(), WalletID: f.wallet.ID, Chain: "eth", TxType: models.TxTypeSweep, Status: "confirmed", Asset: "ETH", Amount: "1000", TxHash: "sweep-tx"}
	f.svc.EnqueueEvent(context.Background(), tx.ID, types.EventType(sweepEvent), map[string]string{"tx_hash": tx.TxHash})

	events := f.storedEvents()
	if len(events) != 1 || *events[0].WebhookConfigID != legacy.ID {
		t.Fatalf("got %d events, want exactly one for the ownerless config", len(events))
	}
}
