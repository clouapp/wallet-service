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

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

const (
	scopedSecret      = "scoped-test-secret"
	withdrawalEvents  = "withdrawal.broadcast"
	deliveryBatchSize = 10
)

type scopedFixture struct {
	svc       *Service
	accountID uuid.UUID
	wallet    models.Wallet
}

func newScopedFixture(t *testing.T) scopedFixture {
	t.Helper()
	mocks.TestDB(t)
	account := mocks.InsertAccount(t, "scoped account")
	wallet := mocks.InsertWalletWithAccount(t, "eth", &account.ID)
	return scopedFixture{svc: newTestWebhookSvc(), accountID: account.ID, wallet: wallet}
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

func insertOwnedConfig(t *testing.T, url string, events []string, accountID, walletID *uuid.UUID) models.WebhookConfig {
	t.Helper()
	return mocks.InsertScopedWebhookConfig(t, url, scopedSecret, events, accountID, walletID)
}

func storedEvents(t *testing.T) []models.WebhookEvent {
	t.Helper()
	var events []models.WebhookEvent
	if err := facades.Orm().Query().Order("created_at").Find(&events); err != nil {
		t.Fatalf("load webhook events: %v", err)
	}
	return events
}

func TestEnqueueScoped_DeliversOnlyToConfigsThatCanSeeTheWallet(t *testing.T) {
	f := newScopedFixture(t)
	otherAccount := mocks.InsertAccount(t, "other account")
	otherWallet := mocks.InsertWallet(t, "eth")

	legacy := insertOwnedConfig(t, "https://legacy.test/hook", []string{withdrawalEvents}, nil, nil)
	owned := insertOwnedConfig(t, "https://owned.test/hook", []string{withdrawalEvents}, &f.accountID, nil)
	walletScoped := insertOwnedConfig(t, "https://wallet.test/hook", []string{withdrawalEvents}, nil, &f.wallet.ID)
	insertOwnedConfig(t, "https://other-account.test/hook", []string{withdrawalEvents}, &otherAccount.ID, nil)
	insertOwnedConfig(t, "https://other-wallet.test/hook", []string{withdrawalEvents}, nil, &otherWallet.ID)
	insertOwnedConfig(t, "https://deposits-only.test/hook", []string{"deposit.confirmed"}, &f.accountID, nil)

	enqueued, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString()))
	if err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if enqueued != 3 {
		t.Fatalf("enqueued = %d, want 3 (legacy, owned, wallet-scoped)", enqueued)
	}

	got := map[uuid.UUID]bool{}
	for _, event := range storedEvents(t) {
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

func TestEnqueueScoped_SameSubjectIsEnqueuedOncePerConfig(t *testing.T) {
	f := newScopedFixture(t)
	insertOwnedConfig(t, "https://owned.test/hook", []string{withdrawalEvents}, &f.accountID, nil)
	subject := uuid.NewString()

	first, err := f.svc.EnqueueScoped(context.Background(), f.event(subject))
	if err != nil || first != 1 {
		t.Fatalf("first enqueue = %d, %v", first, err)
	}
	second, err := f.svc.EnqueueScoped(context.Background(), f.event(subject))
	if err != nil || second != 0 {
		t.Fatalf("second enqueue = %d, %v; want 0 (deduplicated)", second, err)
	}
	if n := len(storedEvents(t)); n != 1 {
		t.Fatalf("stored events = %d, want 1", n)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(storedEvents(t)[0].Payload), &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["id"] != storedEvents(t)[0].ID.String() || payload["type"] != withdrawalEvents {
		t.Fatalf("payload envelope = %+v", payload)
	}
}

func TestEnqueueScoped_RejectsIncompleteEvents(t *testing.T) {
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

func TestDeliverPending_SignsAndMarksDelivered(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	insertOwnedConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

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
	if stored := storedEvents(t)[0]; stored.DeliveryStatus != models.WebhookDeliveryDelivered {
		t.Fatalf("delivery status = %s", stored.DeliveryStatus)
	}

	again, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize)
	if err != nil || again != 0 || len(receiver.bodies) != 1 {
		t.Fatalf("a delivered event must not be sent again: %d, %v, %d", again, err, len(receiver.bodies))
	}
}

func TestDeliverPending_BacksOffAfterAFailureAndGivesUpAtMaxAttempts(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusServiceUnavailable}
	server := httptest.NewServer(receiver)
	defer server.Close()
	insertOwnedConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	if _, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString())); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if delivered, _ := f.svc.DeliverPending(context.Background(), deliveryBatchSize); delivered != 0 {
		t.Fatalf("a 503 must not count as delivered")
	}
	if _, _ = f.svc.DeliverPending(context.Background(), deliveryBatchSize); len(receiver.bodies) != 1 {
		t.Fatalf("the retry must wait for the backoff, got %d sends", len(receiver.bodies))
	}

	stored := storedEvents(t)[0]
	if stored.Attempts != 1 || stored.DeliveryStatus != models.WebhookDeliveryPending || stored.LastError == "" {
		t.Fatalf("after one failure: attempts=%d status=%s error=%q", stored.Attempts, stored.DeliveryStatus, stored.LastError)
	}

	if _, err := facades.Orm().Query().Exec(
		"UPDATE webhook_events SET attempts = max_attempts - 1, updated_at = (NOW() AT TIME ZONE 'UTC') - interval '1 day' WHERE id = ?",
		stored.ID,
	); err != nil {
		t.Fatalf("age event: %v", err)
	}
	if _, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize); err != nil {
		t.Fatalf("DeliverPending: %v", err)
	}
	stored = storedEvents(t)[0]
	if stored.DeliveryStatus != models.WebhookDeliveryFailed || stored.Attempts != stored.MaxAttempts {
		t.Fatalf("after the last attempt: attempts=%d/%d status=%s", stored.Attempts, stored.MaxAttempts, stored.DeliveryStatus)
	}
}

func TestDeliverPending_FailsEventsOfInactiveConfigs(t *testing.T) {
	f := newScopedFixture(t)
	receiver := &recordingReceiver{status: http.StatusOK}
	server := httptest.NewServer(receiver)
	defer server.Close()
	cfg := insertOwnedConfig(t, server.URL, []string{withdrawalEvents}, &f.accountID, nil)

	if _, err := f.svc.EnqueueScoped(context.Background(), f.event(uuid.NewString())); err != nil {
		t.Fatalf("EnqueueScoped: %v", err)
	}
	if _, err := facades.Orm().Query().Model(&models.WebhookConfig{}).Where("id = ?", cfg.ID).Update("is_active", false); err != nil {
		t.Fatalf("deactivate config: %v", err)
	}
	if _, err := f.svc.DeliverPending(context.Background(), deliveryBatchSize); err != nil {
		t.Fatalf("DeliverPending: %v", err)
	}
	if len(receiver.bodies) != 0 || storedEvents(t)[0].DeliveryStatus != models.WebhookDeliveryFailed {
		t.Fatal("events of an inactive config must fail without being sent")
	}
}

func TestUpdateAccountConfig_OwnershipAndClaim(t *testing.T) {
	f := newScopedFixture(t)
	ctx := context.Background()
	otherAccount := mocks.InsertAccount(t, "other account")
	newEvents := []string{"deposit.confirmed", "withdrawal.broadcast", "withdrawal.confirmed", "withdrawal.failed"}

	owned := insertOwnedConfig(t, "https://owned.test/hook", []string{"deposit.confirmed"}, &f.accountID, nil)
	updated, err := f.svc.UpdateAccountConfig(ctx, f.accountID, owned.ID, ConfigUpdate{Events: newEvents})
	if err != nil {
		t.Fatalf("update owned config: %v", err)
	}
	for _, event := range newEvents {
		if !containsEvent(updated.Events, event) {
			t.Errorf("updated events %s miss %s", updated.Events, event)
		}
	}

	if _, err := f.svc.UpdateAccountConfig(ctx, otherAccount.ID, owned.ID, ConfigUpdate{Events: newEvents}); !errors.Is(err, ErrWebhookConfigNotFound) {
		t.Fatalf("another account must not see the config, got %v", err)
	}

	legacy := insertOwnedConfig(t, "https://legacy.test/hook", []string{"deposit.confirmed"}, nil, nil)
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
	if _, err := f.svc.UpdateAccountConfig(ctx, otherAccount.ID, legacy.ID, ConfigUpdate{Events: newEvents, Secret: scopedSecret}); !errors.Is(err, ErrWebhookConfigNotFound) {
		t.Fatalf("a claimed config is no longer claimable, got %v", err)
	}

	walletConfig := insertOwnedConfig(t, "https://wallet.test/hook", []string{"deposit.confirmed"}, nil, &f.wallet.ID)
	if _, err := f.svc.UpdateAccountConfig(ctx, f.accountID, walletConfig.ID, ConfigUpdate{Events: newEvents, Secret: scopedSecret}); !errors.Is(err, ErrWebhookConfigNotFound) {
		t.Fatalf("wallet configs are managed by the wallet endpoints, got %v", err)
	}
}

func TestUpdateAccountConfig_ValidatesTheUpdate(t *testing.T) {
	f := newScopedFixture(t)
	ctx := context.Background()
	owned := insertOwnedConfig(t, "https://owned.test/hook", []string{"deposit.confirmed"}, &f.accountID, nil)

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

func TestEnqueueEvent_LegacyPathNeverReachesAccountOwnedConfigs(t *testing.T) {
	f := newScopedFixture(t)
	otherWallet := mocks.InsertWallet(t, "eth")
	const sweepEvent = "sweep.confirmed"

	legacy := insertOwnedConfig(t, "https://legacy.test/hook", []string{sweepEvent}, nil, nil)
	walletScoped := insertOwnedConfig(t, "https://wallet.test/hook", []string{sweepEvent}, nil, &f.wallet.ID)
	insertOwnedConfig(t, "https://owned.test/hook", []string{sweepEvent}, &f.accountID, nil)
	insertOwnedConfig(t, "https://other-wallet.test/hook", []string{sweepEvent}, nil, &otherWallet.ID)

	tx := mocks.InsertTransaction(t, f.wallet.ID, nil, "eth", models.TxTypeSweep, "confirmed", "ETH", "1000", 1)
	f.svc.EnqueueEvent(context.Background(), tx.ID, types.EventType(sweepEvent), tx)

	got := map[uuid.UUID]bool{}
	for _, event := range storedEvents(t) {
		got[*event.WebhookConfigID] = true
	}
	if len(got) != 2 || !got[legacy.ID] || !got[walletScoped.ID] {
		t.Fatalf("legacy event reached %v, want only the ownerless and matching-wallet configs", got)
	}
}

func TestEnqueueEvent_WalletConfigsSkipEventsWithoutAKnownWallet(t *testing.T) {
	f := newScopedFixture(t)
	const sweepEvent = "sweep.confirmed"

	legacy := insertOwnedConfig(t, "https://legacy.test/hook", []string{sweepEvent}, nil, nil)
	insertOwnedConfig(t, "https://wallet.test/hook", []string{sweepEvent}, nil, &f.wallet.ID)

	tx := mocks.InsertTransaction(t, f.wallet.ID, nil, "eth", models.TxTypeSweep, "confirmed", "ETH", "1000", 1)
	f.svc.EnqueueEvent(context.Background(), tx.ID, types.EventType(sweepEvent), map[string]string{"tx_hash": tx.TxHash})

	events := storedEvents(t)
	if len(events) != 1 || *events[0].WebhookConfigID != legacy.ID {
		t.Fatalf("got %d events, want exactly one for the ownerless config", len(events))
	}
}
