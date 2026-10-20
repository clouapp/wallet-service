package webhook

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

// redeliveryFixture is an account with one owned webhook config on a service whose
// queue sender the test controls.
type redeliveryFixture struct {
	*queueFixture
	accountID uuid.UUID
	cfg       *models.WebhookConfig
}

func newRedeliveryFixture(t *testing.T) redeliveryFixture {
	t.Helper()
	queue := newQueueFixture(t)
	accountID := uuid.New()
	events := []string{string(types.EventDepositConfirmed), string(types.EventWithdrawalConfirmed)}
	cfg, err := queue.svc.CreateConfig(context.Background(), "https://owner.test/hook", "secret", events, &accountID)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	return redeliveryFixture{queueFixture: queue, accountID: accountID, cfg: cfg}
}

// storedEvent inserts an event of cfg about a wallet of the fixture's account,
// created ago before now with status.
func (f redeliveryFixture) storedEvent(t *testing.T, cfg *models.WebhookConfig, eventType types.EventType, status string, ago time.Duration) models.WebhookEvent {
	t.Helper()
	return f.storedEventOf(t, &f.accountID, cfg, eventType, status, ago)
}

// storedEventOf inserts an event of cfg about a wallet of owner; a nil owner is an
// event whose wallet cannot be told (no transaction and no wallet in the payload).
func (f redeliveryFixture) storedEventOf(t *testing.T, owner *uuid.UUID, cfg *models.WebhookConfig, eventType types.EventType, status string, ago time.Duration) models.WebhookEvent {
	t.Helper()
	configID := cfg.ID
	subjectID := uuid.NewString()
	event := &models.WebhookEvent{
		ID:              uuid.New(),
		WebhookConfigID: &configID,
		SubjectID:       &subjectID,
		EventType:       string(eventType),
		Payload:         `{"type":"` + string(eventType) + `"}`,
		DeliveryURL:     cfg.URL,
		DeliveryStatus:  status,
		Attempts:        3,
		MaxAttempts:     10,
	}
	if status == models.WebhookDeliveryDelivered {
		at := time.Now().UTC()
		event.DeliveredAt = &at
	}
	if err := f.store.addEvent(event); err != nil {
		t.Fatal(err)
	}
	f.store.backdate(event.ID.String(), ago)
	if owner != nil {
		f.store.ownEvent(event.ID, *owner)
	}
	return *event
}

// claimedLegacyConfig is a legacy webhook created before ownership was recorded,
// which received the events of every account, then claimed by the fixture's account.
func (f redeliveryFixture) claimedLegacyConfig(t *testing.T) *models.WebhookConfig {
	t.Helper()
	ctx := context.Background()
	events := []string{string(types.EventDepositConfirmed), string(types.EventWalletGasStatusChanged)}
	legacy, err := f.svc.CreateConfig(ctx, "https://legacy.test/hook", "legacy-secret", events, nil)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	active := true
	claimed, err := f.svc.UpdateAccountConfig(ctx, f.accountID, legacy.ID, ConfigUpdate{IsActive: &active, Secret: "legacy-secret"})
	if err != nil {
		t.Fatalf("claim the legacy webhook: %v", err)
	}
	return claimed
}

func TestRedeliver_ClaimedLegacyWebhook_ByIDLeavesTheEventsOfAnotherAccountsWallets(t *testing.T) {
	f := newRedeliveryFixture(t)
	legacy := f.claimedLegacyConfig(t)
	otherAccount := uuid.New()
	own := f.storedEvent(t, legacy, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	foreign := f.storedEventOf(t, &otherAccount, legacy, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	foreignGas := f.storedEventOf(t, &otherAccount, legacy, types.EventWalletGasStatusChanged, models.WebhookDeliveryFailed, time.Hour)
	unknownWallet := f.storedEventOf(t, nil, legacy, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)

	requeued, err := f.svc.Redeliver(context.Background(), f.accountID, legacy.ID, RedeliverySelection{EventIDs: []uuid.UUID{own.ID, foreign.ID, foreignGas.ID, unknownWallet.ID}})
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	if requeued != 1 || len(f.sent) != 1 || f.sent[0].EventID != own.ID.String() {
		t.Fatalf("requeued = %d, queued %v; only the event of the account's own wallet may be sent again", requeued, f.sent)
	}
	if f.stored(own.ID).DeliveryStatus != models.WebhookDeliveryPending {
		t.Fatal("the event of the account's own wallet must be pending again")
	}
	for _, untouched := range []models.WebhookEvent{foreign, foreignGas, unknownWallet} {
		if row := f.stored(untouched.ID); row.DeliveryStatus != untouched.DeliveryStatus || row.Attempts != untouched.Attempts {
			t.Fatalf("event %s of another account's wallet (or of no known wallet) was requeued: %s", untouched.ID, row.DeliveryStatus)
		}
	}
}

func TestRedeliver_ClaimedLegacyWebhook_SinceSelectsOnlyTheAccountsWallets(t *testing.T) {
	f := newRedeliveryFixture(t)
	legacy := f.claimedLegacyConfig(t)
	otherAccount := uuid.New()
	own := f.storedEvent(t, legacy, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	foreign := f.storedEventOf(t, &otherAccount, legacy, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	unknownWallet := f.storedEventOf(t, nil, legacy, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)

	requeued, err := f.svc.Redeliver(context.Background(), f.accountID, legacy.ID, RedeliverySelection{Since: time.Now().UTC().Add(-2 * time.Hour)})
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	if requeued != 1 || len(f.sent) != 1 || f.sent[0].EventID != own.ID.String() {
		t.Fatalf("requeued = %d, queued %v; only the event of the account's own wallet may be sent again", requeued, f.sent)
	}
	for _, untouched := range []models.WebhookEvent{foreign, unknownWallet} {
		if f.stored(untouched.ID).DeliveryStatus != models.WebhookDeliveryDelivered {
			t.Fatalf("event %s outside the account's wallets was requeued", untouched.ID)
		}
	}
}

func TestRedeliver_Since_CountsOnlyTheAccountsEventsAgainstTheLimit(t *testing.T) {
	f := newRedeliveryFixture(t)
	legacy := f.claimedLegacyConfig(t)
	otherAccount := uuid.New()
	for range MaxRedeliveryEvents {
		f.storedEventOf(t, &otherAccount, legacy, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	}
	own := f.storedEvent(t, legacy, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)

	requeued, err := f.svc.Redeliver(context.Background(), f.accountID, legacy.ID, RedeliverySelection{Since: time.Now().UTC().Add(-2 * time.Hour)})
	if err != nil || requeued != 1 || f.stored(own.ID).DeliveryStatus != models.WebhookDeliveryPending {
		t.Fatalf("requeued = %d, %v; another account's events must not count against the limit", requeued, err)
	}
}

func (f redeliveryFixture) stored(id uuid.UUID) models.WebhookEvent {
	for _, event := range f.store.listEvents() {
		if event.ID == id {
			return event
		}
	}
	return models.WebhookEvent{}
}

func TestRedeliver_ByID_RequeuesADeliveredEventAndQueuesIt(t *testing.T) {
	f := newRedeliveryFixture(t)
	delivered := f.storedEvent(t, f.cfg, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	failed := f.storedEvent(t, f.cfg, types.EventWithdrawalConfirmed, models.WebhookDeliveryFailed, time.Hour)

	requeued, err := f.svc.Redeliver(context.Background(), f.accountID, f.cfg.ID, RedeliverySelection{EventIDs: []uuid.UUID{delivered.ID, failed.ID, delivered.ID}})
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	if requeued != 2 {
		t.Fatalf("requeued = %d, want 2", requeued)
	}
	for _, id := range []uuid.UUID{delivered.ID, failed.ID} {
		row := f.stored(id)
		if row.DeliveryStatus != models.WebhookDeliveryPending || row.Attempts != 0 || row.DeliveredAt != nil {
			t.Fatalf("event %s = %s attempts %d delivered %v; want pending with a fresh attempt budget", id, row.DeliveryStatus, row.Attempts, row.DeliveredAt)
		}
	}
	if len(f.sent) != 2 {
		t.Fatalf("queued %d messages, want 2", len(f.sent))
	}
	for _, msg := range f.sent {
		if msg.ConfigID != f.cfg.ID.String() || msg.Attempt != 1 || msg.DeliveryURL != f.cfg.URL {
			t.Fatalf("message = %+v", msg)
		}
	}
}

func TestRedeliver_Since_RequeuesTheEventsOfTheWindowOfTheTypesAsked(t *testing.T) {
	f := newRedeliveryFixture(t)
	inWindow := f.storedEvent(t, f.cfg, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	otherType := f.storedEvent(t, f.cfg, types.EventWithdrawalConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	beforeWindow := f.storedEvent(t, f.cfg, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, 3*time.Hour)
	afterWindow := f.storedEvent(t, f.cfg, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, 10*time.Minute)

	now := time.Now().UTC()
	requeued, err := f.svc.Redeliver(context.Background(), f.accountID, f.cfg.ID, RedeliverySelection{
		Since: now.Add(-2 * time.Hour),
		Until: now.Add(-30 * time.Minute),
		Types: []string{string(types.EventDepositConfirmed)},
	})
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	if requeued != 1 || f.stored(inWindow.ID).DeliveryStatus != models.WebhookDeliveryPending {
		t.Fatalf("requeued = %d; the deposit.confirmed of the window must be pending again", requeued)
	}
	for _, untouched := range []models.WebhookEvent{otherType, beforeWindow, afterWindow} {
		if f.stored(untouched.ID).DeliveryStatus != models.WebhookDeliveryDelivered {
			t.Fatalf("event %s (%s) outside the selection was requeued", untouched.ID, untouched.EventType)
		}
	}
}

func TestRedeliver_Since_WithoutUntilReachesNowAndEveryType(t *testing.T) {
	f := newRedeliveryFixture(t)
	f.storedEvent(t, f.cfg, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	f.storedEvent(t, f.cfg, types.EventWithdrawalConfirmed, models.WebhookDeliveryFailed, 2*time.Minute)

	requeued, err := f.svc.Redeliver(context.Background(), f.accountID, f.cfg.ID, RedeliverySelection{Since: time.Now().UTC().Add(-2 * time.Hour)})
	if err != nil || requeued != 2 {
		t.Fatalf("requeued = %d, %v; want both events", requeued, err)
	}
}

func TestRedeliver_LeavesAnotherConfigsEventAndAnEventQueuedMomentsAgo(t *testing.T) {
	f := newRedeliveryFixture(t)
	otherCfg, err := f.svc.CreateConfig(context.Background(), "https://other.test/hook", "secret", []string{string(types.EventDepositConfirmed)}, &f.accountID)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	ofOtherConfig := f.storedEvent(t, otherCfg, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	inFlight := f.storedEvent(t, f.cfg, types.EventDepositConfirmed, models.WebhookDeliveryPending, 10*time.Second)

	requeued, err := f.svc.Redeliver(context.Background(), f.accountID, f.cfg.ID, RedeliverySelection{EventIDs: []uuid.UUID{ofOtherConfig.ID, inFlight.ID}})
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	if requeued != 0 || len(f.sent) != 0 {
		t.Fatalf("requeued = %d, queued %d; want neither", requeued, len(f.sent))
	}
	if f.stored(ofOtherConfig.ID).DeliveryStatus != models.WebhookDeliveryDelivered || f.stored(inFlight.ID).Attempts != 3 {
		t.Fatal("events outside the request were changed")
	}
}

func TestRedeliver_KeepsTheRowPendingWhenTheQueueSendFails(t *testing.T) {
	f := newRedeliveryFixture(t)
	event := f.storedEvent(t, f.cfg, types.EventDepositConfirmed, models.WebhookDeliveryFailed, time.Hour)
	f.fail[event.ID.String()] = true

	requeued, err := f.svc.Redeliver(context.Background(), f.accountID, f.cfg.ID, RedeliverySelection{EventIDs: []uuid.UUID{event.ID}})
	if err != nil || requeued != 1 {
		t.Fatalf("requeued = %d, %v; the row is requeued and the re-dispatch sends it later", requeued, err)
	}
	if f.stored(event.ID).DeliveryStatus != models.WebhookDeliveryPending {
		t.Fatal("the requeued row must stay pending for the re-dispatch")
	}
}

func TestRedeliver_WithoutAQueueLeavesTheRowToTheLocalOutbox(t *testing.T) {
	svc, store := newMemoryWebhookService()
	accountID := uuid.New()
	cfg, err := svc.CreateConfig(context.Background(), "https://owner.test/hook", "secret", []string{string(types.EventDepositConfirmed)}, &accountID)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	f := redeliveryFixture{queueFixture: &queueFixture{svc: svc, store: store}, accountID: accountID, cfg: cfg}
	event := f.storedEvent(t, cfg, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)

	requeued, err := svc.Redeliver(context.Background(), accountID, cfg.ID, RedeliverySelection{EventIDs: []uuid.UUID{event.ID}})
	if err != nil || requeued != 1 {
		t.Fatalf("requeued = %d, %v", requeued, err)
	}
	due, err := memoryEvents{store: store}.FindDueForDelivery(context.Background(), 10, LocalRetryBaseBackoff, LocalRetryMaxBackoff)
	if err != nil || len(due) != 1 || due[0].ID != event.ID {
		t.Fatalf("the local outbox must find the requeued event due now, got %v (%v)", due, err)
	}
}

func TestRedeliver_Refuses_AWebhookTheAccountDoesNotOwnOrThatIsOff(t *testing.T) {
	f := newRedeliveryFixture(t)
	ctx := context.Background()
	otherAccount := uuid.New()
	walletID := uuid.New()
	legacy, err := f.svc.CreateConfig(ctx, "https://legacy.test/hook", "secret", []string{string(types.EventDepositConfirmed)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	inactive, err := f.svc.CreateConfig(ctx, "https://inactive.test/hook", "secret", []string{string(types.EventDepositConfirmed)}, &f.accountID)
	if err != nil {
		t.Fatal(err)
	}
	f.store.setActive(inactive.ID, false)
	walletCfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://wallet.test/hook", Secret: "secret", Events: pgArray([]string{"deposit.confirmed"}), IsActive: true, WalletID: &walletID}
	if err := f.store.Create(ctx, walletCfg); err != nil {
		t.Fatal(err)
	}
	selection := RedeliverySelection{EventIDs: []uuid.UUID{uuid.New()}}

	cases := []struct {
		name      string
		accountID uuid.UUID
		configID  uuid.UUID
		want      error
	}{
		{"another account's webhook", otherAccount, f.cfg.ID, ErrWebhookConfigNotFound},
		{"an unknown webhook", f.accountID, uuid.New(), ErrWebhookConfigNotFound},
		{"a wallet webhook", f.accountID, walletCfg.ID, ErrWebhookConfigNotFound},
		{"a legacy webhook nobody claimed", f.accountID, legacy.ID, ErrWebhookRedeliveryUnclaimed},
		{"an inactive webhook", f.accountID, inactive.ID, ErrWebhookInactive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.svc.Redeliver(ctx, c.accountID, c.configID, selection); !errors.Is(err, c.want) {
				t.Fatalf("error = %v, want %v", err, c.want)
			}
		})
	}
}

func TestRedeliver_Refuses_ASelectionOutsideItsBounds(t *testing.T) {
	f := newRedeliveryFixture(t)
	now := time.Now().UTC()
	tooMany := make([]uuid.UUID, MaxRedeliveryEvents+1)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}
	cases := []struct {
		name      string
		selection RedeliverySelection
		want      error
	}{
		{"nothing selected", RedeliverySelection{}, ErrRedeliverySelection},
		{"ids and since", RedeliverySelection{EventIDs: []uuid.UUID{uuid.New()}, Since: now.Add(-time.Hour)}, ErrRedeliverySelection},
		{"ids with a type filter", RedeliverySelection{EventIDs: []uuid.UUID{uuid.New()}, Types: []string{"deposit.confirmed"}}, ErrRedeliverySelection},
		{"ids with until", RedeliverySelection{EventIDs: []uuid.UUID{uuid.New()}, Until: now}, ErrRedeliverySelection},
		{"too many ids", RedeliverySelection{EventIDs: tooMany}, ErrRedeliveryTooManyEvents},
		{"since older than the window", RedeliverySelection{Since: now.Add(-RedeliveryWindow - time.Hour)}, ErrRedeliveryWindow},
		{"until before since", RedeliverySelection{Since: now.Add(-time.Hour), Until: now.Add(-2 * time.Hour)}, ErrRedeliveryWindow},
		{"unknown type", RedeliverySelection{Since: now.Add(-time.Hour), Types: []string{"deposit.credited"}}, ErrWebhookUnknownEvent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.svc.Redeliver(context.Background(), f.accountID, f.cfg.ID, c.selection); !errors.Is(err, c.want) {
				t.Fatalf("error = %v, want %v", err, c.want)
			}
		})
	}
}

func TestRedeliver_Since_RefusesMoreEventsThanOneRequestRequeues(t *testing.T) {
	f := newRedeliveryFixture(t)
	for range MaxRedeliveryEvents + 1 {
		f.storedEvent(t, f.cfg, types.EventDepositConfirmed, models.WebhookDeliveryDelivered, time.Hour)
	}

	_, err := f.svc.Redeliver(context.Background(), f.accountID, f.cfg.ID, RedeliverySelection{Since: time.Now().UTC().Add(-2 * time.Hour)})
	if !errors.Is(err, ErrRedeliveryTooManyEvents) {
		t.Fatalf("error = %v, want %v", err, ErrRedeliveryTooManyEvents)
	}
	if len(f.sent) != 0 {
		t.Fatalf("nothing may be queued when the selection is refused, got %d", len(f.sent))
	}
}
