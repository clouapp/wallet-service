package deposit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

type sweepConfirmRow struct {
	id        uuid.UUID
	status    string
	inside    bool
	committed bool
}

type sweepConfirmMemory struct {
	inside  bool
	withins int
	rows    []sweepConfirmRow
}

func (s *sweepConfirmMemory) Create(context.Context, *models.Transaction) error { return nil }
func (s *sweepConfirmMemory) CountByChainAndTxHash(context.Context, string, string, string) (int64, error) {
	return 0, nil
}
func (s *sweepConfirmMemory) CountInternalTransfers(context.Context, string, string, uuid.UUID) (int64, error) {
	return 0, nil
}
func (s *sweepConfirmMemory) FindPendingByChain(context.Context, string) ([]models.Transaction, error) {
	return nil, nil
}
func (s *sweepConfirmMemory) SetBlockNumber(context.Context, uuid.UUID, uint64) error { return nil }

func (s *sweepConfirmMemory) RecordConfirmations(_ context.Context, id uuid.UUID, _ int, status string, _ *time.Time) error {
	s.rows = append(s.rows, sweepConfirmRow{
		id:        id,
		status:    status,
		inside:    s.inside,
		committed: !s.inside,
	})
	return nil
}

func (s *sweepConfirmMemory) committedByID(id uuid.UUID) (sweepConfirmRow, bool) {
	for _, row := range s.rows {
		if row.id == id && row.committed {
			return row, true
		}
	}
	return sweepConfirmRow{}, false
}

// sweepConfirmStore joins confirmation updates to one transaction.
type sweepConfirmStore struct {
	*sweepConfirmMemory
}

// sweepConfirmStoreWithoutTx updates rows and cannot open a transaction.
type sweepConfirmStoreWithoutTx struct {
	*sweepConfirmMemory
}

func (s *sweepConfirmStore) Within(ctx context.Context, fn func(context.Context) error) error {
	s.withins++
	s.inside = true
	err := fn(ctx)
	s.inside = false
	if err != nil {
		kept := s.rows[:0]
		for _, row := range s.rows {
			if row.committed {
				kept = append(kept, row)
			}
		}
		s.rows = kept
		return err
	}
	for i := range s.rows {
		s.rows[i].committed = true
	}
	return nil
}

type sweepConfirmConfigRepo struct {
	configs []models.WebhookConfig
}

func (f *sweepConfirmConfigRepo) Create(context.Context, *models.WebhookConfig) error { return nil }
func (f *sweepConfirmConfigRepo) FindActive(context.Context) ([]models.WebhookConfig, error) {
	return f.configs, nil
}
func (f *sweepConfirmConfigRepo) FindAll(context.Context) ([]models.WebhookConfig, error) {
	return f.configs, nil
}
func (f *sweepConfirmConfigRepo) FindVisibleToAccount(context.Context, uuid.UUID) ([]models.WebhookConfig, error) {
	return f.configs, nil
}
func (f *sweepConfirmConfigRepo) FindByID(context.Context, uuid.UUID) (*models.WebhookConfig, error) {
	return nil, nil
}
func (f *sweepConfirmConfigRepo) AssignAccount(context.Context, uuid.UUID, uuid.UUID, *string, *bool) error {
	return nil
}
func (f *sweepConfirmConfigRepo) DeleteByID(context.Context, uuid.UUID) error { return nil }

type sweepConfirmEventRepo struct {
	created []*models.WebhookEvent
	fail    bool
}

func (f *sweepConfirmEventRepo) Create(_ context.Context, event *models.WebhookEvent) error {
	if f.fail {
		return errors.New("insert webhook event")
	}
	f.created = append(f.created, event)
	return nil
}
func (f *sweepConfirmEventRepo) MarkDelivered(context.Context, string) error { return nil }
func (f *sweepConfirmEventRepo) IncrementAttempt(context.Context, string, string) error {
	return nil
}
func (f *sweepConfirmEventRepo) ExistsForSubject(context.Context, uuid.UUID, string, string) (bool, error) {
	return false, nil
}
func (f *sweepConfirmEventRepo) FindDueForDelivery(context.Context, int, time.Duration, time.Duration) ([]models.WebhookEvent, error) {
	return nil, nil
}
func (f *sweepConfirmEventRepo) MarkFailed(context.Context, string, string) error { return nil }

type sweepConfirmQueue struct {
	store      *sweepConfirmStore
	sent       int
	sentInside bool
}

func (q *sweepConfirmQueue) SendWebhook(context.Context, types.WebhookMessage) error {
	if q.store != nil && q.store.inside {
		q.sentInside = true
	}
	q.sent++
	return nil
}

func sweepConfirmWebhook(events *sweepConfirmEventRepo, sender queue.Sender) *webhook.Service {
	return webhook.NewService(webhook.Deps{
		SQS: sender,
		Configs: &sweepConfirmConfigRepo{configs: []models.WebhookConfig{{
			ID: uuid.New(), URL: "https://example.test/hooks", Secret: "s",
			Events: `{"sweep.confirmed"}`, IsActive: true,
		}}},
		Events: events,
	})
}

func confirmingTx(txType string, required int) models.Transaction {
	return models.Transaction{
		ID:            uuid.New(),
		WalletID:      uuid.New(),
		TxType:        txType,
		Status:        string(types.TxStatusConfirming),
		BlockNumber:   100,
		RequiredConfs: required,
	}
}

func TestApplyConfirmations_CommitsSweepConfirmationWithItsWebhook(t *testing.T) {
	store := &sweepConfirmStore{sweepConfirmMemory: &sweepConfirmMemory{}}
	events := &sweepConfirmEventRepo{}
	sender := &sweepConfirmQueue{store: store}
	svc := &Service{
		txRepo:     store,
		webhookSvc: sweepConfirmWebhook(events, sender),
	}
	sweep := confirmingTx(models.TxTypeSweep, 1)
	stillOpen := confirmingTx(models.TxTypeSweep, 12)
	deposit := confirmingTx(models.TxTypeDeposit, 1)
	deposit.Status = string(types.TxStatusPending)
	withdrawal := confirmingTx(models.TxTypeWithdrawal, 1)
	published := &recordingWithdrawalConfirmations{}
	svc.SetWithdrawalConfirmations(published)
	deposits := &sweepConfirmDeposits{}
	svc.SetDepositEvents(deposits)

	err := svc.applyConfirmations(context.Background(), mocks.NewMockChain("eth"), 100, []models.Transaction{
		sweep, stillOpen, deposit, withdrawal,
	})
	if err != nil {
		t.Fatal(err)
	}

	if store.withins != 1 {
		t.Fatalf("withins=%d", store.withins)
	}
	confirmed, ok := store.committedByID(sweep.ID)
	if !ok || !confirmed.inside || confirmed.status != string(types.TxStatusConfirmed) {
		t.Fatalf("sweep confirmation %+v ok=%v", confirmed, ok)
	}
	open, ok := store.committedByID(stillOpen.ID)
	if !ok || open.inside || open.status != string(types.TxStatusConfirming) {
		t.Fatalf("open sweep %+v ok=%v", open, ok)
	}
	if _, ok := store.committedByID(deposit.ID); !ok {
		t.Fatal("deposit confirmation missing")
	}
	if _, ok := store.committedByID(withdrawal.ID); !ok {
		t.Fatal("withdrawal confirmation missing")
	}
	if deposits.n != 1 || len(published.confirmed) != 1 {
		t.Fatalf("deposits=%d withdrawals=%d", deposits.n, len(published.confirmed))
	}
	if len(events.created) != 1 || events.created[0].EventType != string(types.EventSweepConfirmed) {
		t.Fatalf("events=%d", len(events.created))
	}
	if events.created[0].TransactionID == nil || *events.created[0].TransactionID != sweep.ID {
		t.Fatalf("transaction id %v", events.created[0].TransactionID)
	}
	if sender.sent != 1 || sender.sentInside {
		t.Fatalf("sent=%d sentInside=%v", sender.sent, sender.sentInside)
	}
}

func TestApplyConfirmations_RollsBackSweepConfirmationWhenTheWebhookInsertFails(t *testing.T) {
	store := &sweepConfirmStore{sweepConfirmMemory: &sweepConfirmMemory{}}
	events := &sweepConfirmEventRepo{fail: true}
	sender := &sweepConfirmQueue{store: store}
	svc := &Service{
		txRepo:     store,
		webhookSvc: sweepConfirmWebhook(events, sender),
	}
	sweep := confirmingTx(models.TxTypeSweep, 1)
	deposit := confirmingTx(models.TxTypeDeposit, 1)
	deposits := &sweepConfirmDeposits{}
	svc.SetDepositEvents(deposits)

	err := svc.applyConfirmations(context.Background(), mocks.NewMockChain("eth"), 100, []models.Transaction{sweep, deposit})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.committedByID(sweep.ID); ok || sender.sent != 0 || store.withins != 1 {
		t.Fatalf("sweep kept=%v sent=%d withins=%d", ok, sender.sent, store.withins)
	}
	if _, ok := store.committedByID(deposit.ID); !ok || deposits.n != 1 {
		t.Fatalf("deposit kept=%v published=%d", ok, deposits.n)
	}
}

func TestApplyConfirmations_RefusesSweepConfirmationWithoutATransaction(t *testing.T) {
	memory := &sweepConfirmMemory{}
	events := &sweepConfirmEventRepo{}
	sender := &sweepConfirmQueue{}
	svc := &Service{
		txRepo:     &sweepConfirmStoreWithoutTx{sweepConfirmMemory: memory},
		webhookSvc: sweepConfirmWebhook(events, sender),
	}
	sweep := confirmingTx(models.TxTypeSweep, 1)

	err := svc.applyConfirmations(context.Background(), mocks.NewMockChain("eth"), 100, []models.Transaction{sweep})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := memory.committedByID(sweep.ID); ok || sender.sent != 0 || len(events.created) != 0 || memory.withins != 0 {
		t.Fatalf("kept=%v sent=%d events=%d withins=%d", ok, sender.sent, len(events.created), memory.withins)
	}
}

func TestApplyConfirmations_ConfirmsASweepWithoutAWebhookWriter(t *testing.T) {
	store := &sweepConfirmStore{sweepConfirmMemory: &sweepConfirmMemory{}}
	svc := &Service{txRepo: store}
	sweep := confirmingTx(models.TxTypeSweep, 1)

	err := svc.applyConfirmations(context.Background(), mocks.NewMockChain("eth"), 100, []models.Transaction{sweep})
	if err != nil {
		t.Fatal(err)
	}
	row, ok := store.committedByID(sweep.ID)
	if !ok || row.inside || store.withins != 0 || row.status != string(types.TxStatusConfirmed) {
		t.Fatalf("row %+v ok=%v withins=%d", row, ok, store.withins)
	}
}

type sweepConfirmDeposits struct{ n int }

func (r *sweepConfirmDeposits) Publish(context.Context, types.EventType, models.Transaction) error {
	r.n++
	return nil
}
