package withdrawalevents

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/webhook"
)

type guardedCallbackStore struct {
	*fakeWithdrawals
	predecessor      map[uuid.UUID]*models.Withdrawal
	failedGuardCalls int
	raceAtFailure    bool
	beforeCommitted  func()
	enqueuer         *fakeEnqueuer
	committed        bool
}

func (s *guardedCallbackStore) FindByOriginalRecoveryTransactionID(_ context.Context, id uuid.UUID) (*models.Withdrawal, error) {
	return s.predecessor[id], nil
}
func (s *guardedCallbackStore) MarkFailedForTransaction(ctx context.Context, id, transactionID uuid.UUID, code string) error {
	s.failedGuardCalls++
	w := s.byID[id]
	if s.raceAtFailure {
		other := uuid.New()
		w.TransactionID = &other
	}
	if w != nil && w.TransactionID != nil && *w.TransactionID != transactionID {
		return models.ErrTransactionSettled
	}
	return s.MarkFailed(ctx, id, code)
}
func (s *guardedCallbackStore) MarkConfirmed(ctx context.Context, id, transactionID uuid.UUID) error {
	w := s.byID[id]
	if w != nil && w.TransactionID != nil && *w.TransactionID != transactionID {
		return models.ErrTransactionSettled
	}
	if err := s.fakeWithdrawals.MarkConfirmed(ctx, id, transactionID); err != nil {
		return err
	}
	if w != nil {
		w.Status = models.WithdrawalStatusConfirmed
		w.TransactionID = &transactionID
	}
	return nil
}
func (s *guardedCallbackStore) WithinCommitted(ctx context.Context, fn func(context.Context) error) error {
	if s.beforeCommitted != nil {
		s.beforeCommitted()
	}
	previous := map[uuid.UUID]models.Withdrawal{}
	for id, w := range s.byID {
		previous[id] = *w
	}
	events := append([]webhook.ScopedEvent(nil), s.enqueuer.events...)
	previousUpdates := map[uuid.UUID]map[string]any{}
	for id, columns := range s.updates {
		previousUpdates[id] = columns
	}
	err := fn(ctx)
	if err != nil {
		for id, w := range previous {
			*s.byID[id] = w
		}
		s.updates = previousUpdates
		s.enqueuer.events = events
		return err
	}
	s.committed = true
	return nil
}

func guardedFixture() (*fixture, *guardedCallbackStore) {
	f := newFixture()
	store := &guardedCallbackStore{fakeWithdrawals: f.withdrawals, predecessor: map[uuid.UUID]*models.Withdrawal{}, enqueuer: f.enqueuer}
	f.publisher.withdrawals = store
	return f, store
}

func TestRecoveryOldConfirmationByIdempotencyIsIgnored(t *testing.T) {
	f := newFixture()
	f.confirmTx()
	delete(f.withdrawals.byTransaction, f.tx.ID)
	candidate := uuid.New()
	f.withdrawal.TransactionID = &candidate
	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err != nil {
		t.Fatal(err)
	}
	if len(f.enqueuer.events) != 0 || len(f.withdrawals.updates) != 0 || *f.withdrawal.TransactionID != candidate {
		t.Fatal("old confirmation changed or announced the candidate")
	}
}

func TestRecoveryOldFailureCannotReplaceAlreadyFailedCandidateReason(t *testing.T) {
	f := newFixture()
	delete(f.withdrawals.byTransaction, f.tx.ID)
	candidate := uuid.New()
	code := "candidate_onchain_reverted"
	f.withdrawal.TransactionID = &candidate
	f.withdrawal.Status = models.WithdrawalStatusFailed
	f.withdrawal.FailureReason = &code
	send, err := f.publisher.StageFailed(context.Background(), f.tx, "old_broadcast_refused")
	if err != nil || send != nil || len(f.enqueuer.events) != 0 || len(f.withdrawals.updates) != 0 || *f.withdrawal.FailureReason != code {
		t.Fatalf("stale failure was not a noop: %v", err)
	}
}

func TestRecoveryPredecessorWithoutIdempotencyDoesNotBecomeOrphanEvent(t *testing.T) {
	f, store := guardedFixture()
	delete(store.byTransaction, f.tx.ID)
	f.tx.IdempotencyKey = nil
	candidate := uuid.New()
	f.withdrawal.TransactionID = &candidate
	store.predecessor[f.tx.ID] = f.withdrawal
	f.confirmTx()
	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err != nil {
		t.Fatal(err)
	}
	send, err := f.publisher.StageFailed(context.Background(), f.tx, "old_failure")
	if err != nil || send != nil || len(f.enqueuer.events) != 0 || len(store.updates) != 0 {
		t.Fatal("known recovery predecessor was published as an orphan")
	}
}

func TestRecoveryFailureRaceReturnsRollbackSignal(t *testing.T) {
	f, store := guardedFixture()
	store.raceAtFailure = true
	send, err := f.publisher.StageFailed(context.Background(), f.tx, "old_failure")
	if !errors.Is(err, models.ErrTransactionSettled) || send != nil || store.failedGuardCalls != 1 || len(store.updates) != 0 {
		t.Fatalf("stale guarded failure did not stop outer commit: %v", err)
	}
	// The staged delivery is tentative here; the real-PG test proves that the
	// enclosing transaction rolls it back when this signal is returned.
	if f.enqueuer.sends != 0 {
		t.Fatal("tentative event was queued")
	}
}

func TestRecoveryConfirmedCASRacePublishesNothing(t *testing.T) {
	f, store := guardedFixture()
	f.confirmTx()
	candidate := uuid.New()
	store.beforeCommitted = func() { f.withdrawal.TransactionID = &candidate }
	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err != nil {
		t.Fatal(err)
	}
	if len(f.enqueuer.events) != 0 || f.enqueuer.sends != 0 || len(store.updates) != 0 || *f.withdrawal.TransactionID != candidate {
		t.Fatal("confirmation escaped the failed CAS")
	}
}

func TestRecoveryConfirmedOutboxFailureRollsBackOutcome(t *testing.T) {
	f, store := guardedFixture()
	f.confirmTx()
	f.enqueuer.err = errors.New("outbox unavailable")
	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err == nil {
		t.Fatal("outbox failure accepted")
	}
	if f.withdrawal.Status != models.WithdrawalStatusBroadcast || len(store.updates) != 0 || len(f.enqueuer.events) != 0 || f.enqueuer.sends != 0 {
		t.Fatal("outcome escaped outbox rollback")
	}
}

func TestRecoveryConfirmedQueuesOnlyAfterOwnedCommit(t *testing.T) {
	f, store := guardedFixture()
	f.confirmTx()
	if err := f.publisher.MarkConfirmed(context.Background(), f.tx); err != nil {
		t.Fatal(err)
	}
	if !store.committed || f.withdrawal.Status != models.WithdrawalStatusConfirmed || len(f.enqueuer.events) != 1 || f.enqueuer.sends != 1 {
		t.Fatal("confirmed outcome and event were not committed together")
	}
}
