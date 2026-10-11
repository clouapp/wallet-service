package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// lostDeposit inserts a deposit on chainID the tracker confirmed confirmedAgo ago
// without any deposit.confirmed row, as a publish that failed after the status was
// persisted leaves it.
func (f depositEventsFixture) lostDeposit(t *testing.T, chainID string, confirmedAgo time.Duration) models.Transaction {
	t.Helper()
	tx := fixtures.InsertTransaction(t, f.wallet.ID, nil, chainID, models.TxTypeDeposit, "confirmed", "eth", halfEtherBaseUnits, depositBlock)
	confirmedAt := time.Now().UTC().Add(-confirmedAgo).Truncate(time.Microsecond)
	if _, err := facades.Orm().Query().Exec(
		`UPDATE transactions SET confirmations = 3, confirmed_at = ? WHERE id = ?`, confirmedAt, tx.ID,
	); err != nil {
		t.Fatalf("confirm deposit: %v", err)
	}
	tx.Status = "confirmed"
	tx.Confirmations = 3
	tx.ConfirmedAt = &confirmedAt
	return tx
}

// configCreated inserts a deposit.confirmed config of the fixture account created
// createdAgo ago.
func (f depositEventsFixture) configCreated(t *testing.T, url string, createdAgo time.Duration) models.WebhookConfig {
	t.Helper()
	cfg := fixtures.InsertScopedWebhookConfig(t, url, depositHookSecret, []string{depositConfirmedEvent}, &f.accountID, nil)
	if _, err := facades.Orm().Query().Exec(
		`UPDATE webhook_configs SET created_at = (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?) WHERE id = ?`,
		createdAgo.Seconds(), cfg.ID,
	); err != nil {
		t.Fatalf("backdate webhook config: %v", err)
	}
	return cfg
}

func confirmedRowsFor(t *testing.T, configID uuid.UUID) []models.WebhookEvent {
	t.Helper()
	var rows []models.WebhookEvent
	for _, row := range depositWebhookRows(t) {
		if row.WebhookConfigID != nil && *row.WebhookConfigID == configID {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestBackfill_DepositEvents_RepublishesAConfirmationWhoseEventWasLost(t *testing.T) {
	f := newDepositEventsFixture(t)
	cfg := f.configCreated(t, "https://owner.test/hook", time.Hour)
	tx := f.lostDeposit(t, "eth", 10*time.Minute)

	republished, err := f.svc.BackfillDepositEvents(context.Background(), nil)
	if err != nil {
		t.Fatalf("BackfillDepositEvents: %v", err)
	}
	if republished != 1 {
		t.Fatalf("republished = %d, want 1", republished)
	}
	rows := confirmedRowsFor(t, cfg.ID)
	if len(rows) != 1 {
		t.Fatalf("deposit.confirmed rows = %d, want 1", len(rows))
	}
	var envelope struct {
		Type string                `json:"type"`
		Data depositevents.Payload `json:"data"`
	}
	if decodeErr := json.Unmarshal([]byte(rows[0].Payload), &envelope); decodeErr != nil {
		t.Fatalf("decode payload: %v", decodeErr)
	}
	if envelope.Type != depositConfirmedEvent || envelope.Data.TransactionID != tx.ID.String() || envelope.Data.Status != "confirmed" {
		t.Fatalf("envelope = %+v", envelope)
	}
	if envelope.Data.Amount != "0.5" || envelope.Data.ConfirmedAt == nil {
		t.Fatalf("payload = %+v", envelope.Data)
	}

	again, err := f.svc.BackfillDepositEvents(context.Background(), nil)
	if err != nil || again != 0 {
		t.Fatalf("second backfill = %d, %v; the gap is closed", again, err)
	}
	if rows := confirmedRowsFor(t, cfg.ID); len(rows) != 1 {
		t.Fatalf("deposit.confirmed rows after a second run = %d, want 1", len(rows))
	}
}

func TestBackfill_DepositEvents_LeavesOutAConfigCreatedAfterTheConfirmation(t *testing.T) {
	f := newDepositEventsFixture(t)
	existing := f.configCreated(t, "https://existing.test/hook", time.Hour)
	f.lostDeposit(t, "eth", 10*time.Minute)
	later := f.configCreated(t, "https://later.test/hook", time.Minute)

	if _, err := f.svc.BackfillDepositEvents(context.Background(), nil); err != nil {
		t.Fatalf("BackfillDepositEvents: %v", err)
	}
	if rows := confirmedRowsFor(t, existing.ID); len(rows) != 1 {
		t.Fatalf("the config that existed at confirmation got %d rows, want 1", len(rows))
	}
	if rows := confirmedRowsFor(t, later.ID); len(rows) != 0 {
		t.Fatalf("a config created after the confirmation got %d rows, want none", len(rows))
	}
}

func TestBackfill_DepositEvents_OnlyTheGivenChainsAndOnlyPastTheGrace(t *testing.T) {
	f := newDepositEventsFixture(t)
	cfg := f.configCreated(t, "https://owner.test/hook", time.Hour)
	onEth := f.lostDeposit(t, "eth", 10*time.Minute)
	f.lostDeposit(t, "sol", 10*time.Minute)
	f.lostDeposit(t, "eth", time.Second)

	republished, err := f.svc.BackfillDepositEvents(context.Background(), []string{"eth"})
	if err != nil {
		t.Fatalf("BackfillDepositEvents: %v", err)
	}
	rows := confirmedRowsFor(t, cfg.ID)
	if republished != 1 || len(rows) != 1 || *rows[0].SubjectID != onEth.ID.String() {
		t.Fatalf("republished = %d rows = %d; only the eth deposit past the grace is repaired", republished, len(rows))
	}
}

func TestBackfill_DepositEvents_RequiresAPublisher(t *testing.T) {
	svc, _ := setupDepositService(t)
	if _, err := svc.BackfillDepositEvents(context.Background(), nil); err == nil {
		t.Fatal("without a deposit events publisher the backfill must fail, not report success")
	}
}

// failingOnce loses the first publish of each deposit event, as an outbox insert
// that fails after the tracker persisted the status does.
type failingOnce struct {
	*depositevents.Publisher
	failed map[string]bool
}

func (f *failingOnce) Publish(ctx context.Context, eventType types.EventType, tx models.Transaction) error {
	key := string(eventType) + tx.ID.String()
	if !f.failed[key] {
		f.failed[key] = true
		return errors.New("outbox unavailable")
	}
	return f.Publisher.Publish(ctx, eventType, tx)
}

func TestRun_ConfirmationCheck_RepairsADepositConfirmedWhosePublishFailed(t *testing.T) {
	f := newDepositEventsFixture(t)
	cfg := f.configCreated(t, "https://owner.test/hook", time.Hour)
	f.svc.SetDepositEvents(&failingOnce{Publisher: f.publisher, failed: map[string]bool{}})
	tx := fixtures.InsertTransaction(t, f.wallet.ID, nil, "eth", models.TxTypeDeposit, "pending", "eth", halfEtherBaseUnits, depositBlock)

	if err := f.svc.updateConfirmations(context.Background(), "eth", f.adapter, confirmedAtBlock); err != nil {
		t.Fatalf("updateConfirmations: %v", err)
	}
	var confirmed models.Transaction
	if err := facades.Orm().Query().Where("id = ?", tx.ID).First(&confirmed); err != nil {
		t.Fatalf("load deposit: %v", err)
	}
	if confirmed.Status != "confirmed" {
		t.Fatalf("status = %q, want confirmed", confirmed.Status)
	}
	if rows := confirmedRowsFor(t, cfg.ID); len(rows) != 0 {
		t.Fatalf("the failed publish must leave no row, got %d", len(rows))
	}

	f.svc.now = func() time.Time { return time.Now().UTC().Add(2 * depositEventBackfillGrace) }
	if err := f.svc.RunConfirmationCheck(context.Background()); err != nil {
		t.Fatalf("RunConfirmationCheck: %v", err)
	}
	if rows := confirmedRowsFor(t, cfg.ID); len(rows) != 1 {
		t.Fatalf("deposit.confirmed rows after the tracker run = %d, want 1", len(rows))
	}
}
