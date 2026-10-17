package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const depositConfirmingEvent = "deposit.confirming"

// progressFixture is a pending deposit of the fixture wallet on eth, in block
// depositBlock, and an account webhook created an hour ago that subscribes to
// deposit.confirming and deposit.confirmed.
type progressFixture struct {
	depositEventsFixture
	cfg models.WebhookConfig
	tx  models.Transaction
}

func newProgressFixture(t *testing.T, requiredConfs int) progressFixture {
	t.Helper()
	f := newDepositEventsFixture(t)
	cfg := fixtures.InsertScopedWebhookConfig(t, "https://owner.test/hook", depositHookSecret,
		[]string{depositConfirmingEvent, depositConfirmedEvent}, &f.accountID, nil)
	if _, err := facades.Orm().Query().Exec(
		`UPDATE webhook_configs SET created_at = (NOW() AT TIME ZONE 'UTC') - INTERVAL '1 hour' WHERE id = ?`, cfg.ID,
	); err != nil {
		t.Fatalf("backdate webhook config: %v", err)
	}
	tx := insertRecordedUnder(t, f.wallet.ID, "eth", models.TxTypeDeposit, "pending", models.FinalityConfirmations, depositBlock, requiredConfs)
	return progressFixture{depositEventsFixture: f, cfg: cfg, tx: tx}
}

// pass runs one confirmation pass of the chain with its tip at tip.
func (f progressFixture) pass(t *testing.T, tip uint64) {
	t.Helper()
	if err := f.svc.updateConfirmations(context.Background(), "eth", f.adapter, tip); err != nil {
		t.Fatalf("confirmation pass at tip %d: %v", tip, err)
	}
}

// outboxRow is one webhook_events row of the fixture webhook and the deposit data it carries.
type outboxRow struct {
	row  models.WebhookEvent
	data depositevents.Payload
}

// outbox returns the fixture webhook's rows of eventType, fewest confirmations first.
func (f progressFixture) outbox(t *testing.T, eventType string) []outboxRow {
	t.Helper()
	var rows []models.WebhookEvent
	if err := facades.Orm().Query().Where("webhook_config_id = ? AND event_type = ?", f.cfg.ID, eventType).Find(&rows); err != nil {
		t.Fatalf("load %s rows: %v", eventType, err)
	}
	out := make([]outboxRow, 0, len(rows))
	for _, row := range rows {
		var envelope struct {
			Data depositevents.Payload `json:"data"`
		}
		if err := json.Unmarshal([]byte(row.Payload), &envelope); err != nil {
			t.Fatalf("decode %s payload: %v", eventType, err)
		}
		out = append(out, outboxRow{row: row, data: envelope.Data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].data.Confirmations < out[j].data.Confirmations })
	return out
}

func confirmationsOf(rows []outboxRow) []int {
	counts := make([]int, len(rows))
	for i, row := range rows {
		counts[i] = row.data.Confirmations
	}
	return counts
}

func TestUpdate_Confirmations_PublishesDepositConfirmingEachTimeTheCountRises(t *testing.T) {
	f := newProgressFixture(t, 6)

	f.pass(t, 100) // 1 confirmation: pending → confirming
	f.pass(t, 100) // still 1: nothing new to say
	f.pass(t, 102) // 3
	f.pass(t, 104) // 5
	f.pass(t, 105) // 6 of 6: confirmed
	f.pass(t, 107) // confirmed rows are not passed again

	confirming := f.outbox(t, depositConfirmingEvent)
	if got := confirmationsOf(confirming); !slices.Equal(got, []int{1, 3, 5}) {
		t.Fatalf("deposit.confirming sent at %v confirmations, want [1 3 5]", got)
	}
	for _, r := range confirming {
		subject := fmt.Sprintf("%s:%d", f.tx.ID, r.data.Confirmations)
		if r.row.SubjectID == nil || *r.row.SubjectID != subject {
			t.Fatalf("deposit.confirming row subject %v, want %s", r.row.SubjectID, subject)
		}
		if r.row.TransactionID == nil || *r.row.TransactionID != f.tx.ID || r.data.TransactionID != f.tx.ID.String() {
			t.Fatalf("deposit.confirming row must carry the plain transaction id, got row %v data %q", r.row.TransactionID, r.data.TransactionID)
		}
		if r.data.Status != string(types.TxStatusConfirming) || r.data.RequiredConfirmations != 6 {
			t.Fatalf("deposit.confirming data status %q, %d required", r.data.Status, r.data.RequiredConfirmations)
		}
	}

	confirmed := f.outbox(t, depositConfirmedEvent)
	if len(confirmed) != 1 {
		t.Fatalf("got %d deposit.confirmed rows, want 1", len(confirmed))
	}
	if *confirmed[0].row.SubjectID != f.tx.ID.String() || confirmed[0].data.Confirmations != 6 || confirmed[0].data.Status != string(types.TxStatusConfirmed) {
		t.Fatalf("deposit.confirmed subject %q, %d confirmations, status %q", *confirmed[0].row.SubjectID, confirmed[0].data.Confirmations, confirmed[0].data.Status)
	}
}

func TestUpdate_Confirmations_ACountAlreadySentIsNotSentAgain(t *testing.T) {
	f := newProgressFixture(t, 6)
	f.pass(t, 102) // 3 confirmations

	// A scanner pass that read the row before the tracker moved it publishes the same count.
	if err := f.publisher.Publish(context.Background(), types.EventDepositConfirming, reloadTransaction(t, f.tx.ID)); err != nil {
		t.Fatalf("publish the same count again: %v", err)
	}
	// A lagging height provider moves the count back; the next pass reaches 3 again.
	f.pass(t, 101)
	f.pass(t, 102)

	if got := confirmationsOf(f.outbox(t, depositConfirmingEvent)); !slices.Equal(got, []int{3}) {
		t.Fatalf("deposit.confirming rows at %v confirmations, want one at 3", got)
	}
}

// depositEventsOf lists the events published for tx as "<event>@<confirmations>", in order.
func depositEventsOf(log *depositEventLog, tx models.Transaction) []string {
	var out []string
	for _, published := range log.published {
		if published.tx.ID == tx.ID {
			out = append(out, fmt.Sprintf("%s@%d", published.event, published.tx.Confirmations))
		}
	}
	return out
}

// Under a native finality the count keeps rising while the final block is not
// read; progress stops once the count has reached the required confirmations, so a
// stalled finality read cannot send one event per pass until it recovers.
func TestFinality_StalledFinality_StopsTheConfirmingProgressAtTheRequiredCount(t *testing.T) {
	svc, adapter, events := finalityChain(t, models.ChainTTron, models.FinalitySolidified)
	solidified := uint64(0)
	adapter.SolidifiedBlockFn = func(context.Context) (uint64, error) {
		if solidified == 0 {
			return 0, errors.New("solidified block unavailable")
		}
		return solidified, nil
	}
	wallet := fixtures.InsertWallet(t, models.ChainTTron)
	tx := insertRecordedUnder(t, wallet.ID, models.ChainTTron, models.TxTypeDeposit, "pending", models.FinalitySolidified, 100, 3)
	ctx := context.Background()

	for _, tip := range []uint64{100, 101, 103, 104, 110, 150} {
		if err := svc.updateConfirmations(ctx, models.ChainTTron, adapter, tip); err != nil {
			t.Fatalf("pass at tip %d: %v", tip, err)
		}
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirming), 51)

	solidified = 100
	if err := svc.updateConfirmations(ctx, models.ChainTTron, adapter, 151); err != nil {
		t.Fatal(err)
	}
	requireTransactionState(t, tx.ID, string(types.TxStatusConfirmed), 52)

	want := []string{"deposit.confirming@1", "deposit.confirming@2", "deposit.confirming@4", "deposit.confirmed@52"}
	if got := depositEventsOf(events, tx); !slices.Equal(got, want) {
		t.Fatalf("published %v, want %v", got, want)
	}
}

// confirmedLost loses every deposit.confirmed publish, as an outbox insert that
// fails after the tracker persisted the confirmation does.
type confirmedLost struct {
	*depositevents.Publisher
}

func (p confirmedLost) Publish(ctx context.Context, eventType types.EventType, tx models.Transaction) error {
	if eventType == types.EventDepositConfirmed {
		return errors.New("outbox unavailable")
	}
	return p.Publisher.Publish(ctx, eventType, tx)
}

func TestBackfill_DepositEvents_RepairsALostConfirmationBesideItsConfirmingRows(t *testing.T) {
	f := newProgressFixture(t, 6)
	f.svc.SetDepositEvents(confirmedLost{Publisher: f.publisher})
	f.pass(t, 100)
	f.pass(t, 103)
	f.pass(t, 105)
	if got := confirmationsOf(f.outbox(t, depositConfirmingEvent)); !slices.Equal(got, []int{1, 4}) {
		t.Fatalf("deposit.confirming rows at %v confirmations, want [1 4]", got)
	}
	if rows := f.outbox(t, depositConfirmedEvent); len(rows) != 0 {
		t.Fatalf("the lost publish must leave no deposit.confirmed row, got %d", len(rows))
	}

	f.svc.SetDepositEvents(f.publisher)
	f.svc.now = func() time.Time { return time.Now().UTC().Add(2 * depositEventBackfillGrace) }
	republished, err := f.svc.BackfillDepositEvents(context.Background(), nil)
	if err != nil || republished != 1 {
		t.Fatalf("BackfillDepositEvents = %d, %v; want the one confirmation repaired", republished, err)
	}
	confirmed := f.outbox(t, depositConfirmedEvent)
	if len(confirmed) != 1 || *confirmed[0].row.SubjectID != f.tx.ID.String() || confirmed[0].data.Confirmations != 6 {
		t.Fatalf("deposit.confirmed rows after the backfill: %+v", confirmed)
	}
	if again, err := f.svc.BackfillDepositEvents(context.Background(), nil); err != nil || again != 0 {
		t.Fatalf("second backfill = %d, %v; the gap is closed", again, err)
	}
	if got := confirmationsOf(f.outbox(t, depositConfirmingEvent)); !slices.Equal(got, []int{1, 4}) {
		t.Fatalf("the backfill changed the deposit.confirming rows: %v", got)
	}
}

func TestRedeliver_Requeues_EachConfirmingProgressRowUnderItsOwnID(t *testing.T) {
	f := newProgressFixture(t, 6)
	f.pass(t, 100)
	f.pass(t, 103)
	before := f.outbox(t, depositConfirmingEvent)
	if len(before) != 2 {
		t.Fatalf("got %d deposit.confirming rows, want 2", len(before))
	}
	// Delivered ten minutes ago: created_at holds whole seconds, so a row created in
	// the second the request asks would fall after its upper bound.
	if _, err := facades.Orm().Query().Exec(
		`UPDATE webhook_events SET delivery_status = ?, delivered_at = (NOW() AT TIME ZONE 'UTC'),
			created_at = (NOW() AT TIME ZONE 'UTC') - INTERVAL '10 minutes', updated_at = (NOW() AT TIME ZONE 'UTC') - INTERVAL '10 minutes'
		WHERE webhook_config_id = ?`,
		models.WebhookDeliveryDelivered, f.cfg.ID,
	); err != nil {
		t.Fatalf("mark the rows delivered: %v", err)
	}

	requeued, err := f.svc.webhookSvc.Redeliver(context.Background(), f.accountID, f.cfg.ID, webhook.RedeliverySelection{
		Since: time.Now().UTC().Add(-time.Hour),
		Types: []string{depositConfirmingEvent},
	})
	if err != nil || requeued != 2 {
		t.Fatalf("Redeliver = %d, %v; want both deposit.confirming rows", requeued, err)
	}
	after := f.outbox(t, depositConfirmingEvent)
	if len(after) != len(before) {
		t.Fatalf("a redelivery must not add rows: %d before, %d after", len(before), len(after))
	}
	for i := range after {
		if after[i].row.ID != before[i].row.ID || *after[i].row.SubjectID != *before[i].row.SubjectID {
			t.Fatalf("row %d changed identity: %s %q → %s %q", i, before[i].row.ID, *before[i].row.SubjectID, after[i].row.ID, *after[i].row.SubjectID)
		}
		if after[i].row.DeliveryStatus != models.WebhookDeliveryPending {
			t.Fatalf("row %s is %s after the redelivery, want pending", after[i].row.ID, after[i].row.DeliveryStatus)
		}
	}
}
