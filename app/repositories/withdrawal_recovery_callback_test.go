package repositories_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/pkg/types"
)

type callbackOutbox struct {
	afterStage   func(context.Context) error
	sends        int
	enqueueCalls int
	atSend       func()
}

func (o *callbackOutbox) EnqueueScoped(ctx context.Context, event webhook.ScopedEvent) (int, error) {
	o.enqueueCalls++
	send, n, err := o.StageScoped(ctx, event)
	if err == nil && send != nil {
		send(ctx)
	}
	return n, err
}
func (o *callbackOutbox) StageScoped(ctx context.Context, event webhook.ScopedEvent) (func(context.Context), int, error) {
	payload, _ := json.Marshal(event.Data)
	subject := event.SubjectID
	err := repositories.NewWebhookEventRepository(nil).Create(ctx, &models.WebhookEvent{ID: uuid.New(), TransactionID: event.TransactionID, SubjectID: &subject, EventType: string(event.EventType), Payload: string(payload), DeliveryURL: "https://callbacks.invalid/test", DeliveryStatus: "pending", MaxAttempts: 10})
	if err != nil {
		return nil, 0, err
	}
	if o.afterStage != nil {
		if err := o.afterStage(ctx); err != nil {
			return nil, 0, err
		}
	}
	return func(context.Context) {
		o.sends++
		if o.atSend != nil {
			o.atSend()
		}
	}, 1, nil
}

type callbackCommittedRace struct {
	*repositories.WithdrawalRepository
	before func()
}

func (r callbackCommittedRace) WithinCommitted(ctx context.Context, fn func(context.Context) error) error {
	if r.before != nil {
		r.before()
	}
	return r.WithdrawalRepository.WithinCommitted(ctx, fn)
}

func callbackPublisher(store withdrawalevents.WithdrawalStore, outbox *callbackOutbox) *withdrawalevents.Publisher {
	return withdrawalevents.NewPublisher(withdrawalevents.PublisherDeps{Withdrawals: store, Transactions: repositories.NewTransactionRepository(nil), Wallets: repositories.NewWalletRepository(nil), Enqueuer: outbox})
}

func callbackEventCount(t *testing.T) int64 {
	t.Helper()
	count, err := facades.Orm().Query().Model(&models.WebhookEvent{}).Count()
	require.NoError(t, err)
	return count
}

func TestRecoveryLateCallbacksCannotOverwriteAlreadyFailedCandidate(t *testing.T) {
	recovery, row, b := recoveryRows(t)
	ctx := context.Background()
	won, err := recovery.Claim(ctx, row)
	require.NoError(t, err)
	require.True(t, won)
	call := `{"nonce":0}`
	won, err = recovery.Transition(ctx, row.ID, models.RecoveryClaimed, models.RecoverySigning, models.WithdrawalRecoveryUpdate{CallJSON: &call})
	require.NoError(t, err)
	require.True(t, won)
	row.State = models.RecoverySigning
	row.CallJSON = call
	candidate := recoveryCandidate(row, b)
	won, err = recovery.RecordCandidate(ctx, row, candidate, "sealed-bytes")
	require.NoError(t, err)
	require.True(t, won)
	_, err = facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", candidate.ID).Update(map[string]any{"status": "failed", "error_message": "onchain_reverted"})
	require.NoError(t, err)
	store := repositories.NewWithdrawalRepository(nil)
	require.NoError(t, store.MarkFailedForTransaction(ctx, b.OperationID, candidate.ID, "candidate_onchain_reverted"))
	original, err := repositories.NewTransactionRepository(nil).FindByID(ctx, b.OriginalTransactionID)
	require.NoError(t, err)
	require.Nil(t, original.IdempotencyKey)
	outbox := &callbackOutbox{}
	publisher := callbackPublisher(store, outbox)
	send, err := publisher.StageFailed(ctx, original, "old_broadcast_refused")
	require.NoError(t, err)
	require.Nil(t, send)
	original.Status = "confirmed"
	require.NoError(t, publisher.MarkConfirmed(ctx, original))
	require.ErrorIs(t, store.MarkFailedForTransaction(ctx, b.OperationID, b.OriginalTransactionID, "stale_old_reason"), models.ErrTransactionSettled)
	require.ErrorIs(t, store.MarkConfirmed(ctx, b.OperationID, b.OriginalTransactionID), models.ErrTransactionSettled)
	saved, err := store.FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, candidate.ID, *saved.TransactionID)
	require.Equal(t, "failed", saved.Status)
	require.Equal(t, "candidate_onchain_reverted", *saved.FailureReason)
	require.Zero(t, callbackEventCount(t))
	require.Zero(t, outbox.sends)
	require.Zero(t, outbox.enqueueCalls)
}

func TestRecoveryFailureCASRaceRollsBackStagedOutbox(t *testing.T) {
	_, row, b := recoveryRows(t)
	ctx := context.Background()
	store := repositories.NewWithdrawalRepository(nil)
	original, err := repositories.NewTransactionRepository(nil).FindByID(ctx, b.OriginalTransactionID)
	require.NoError(t, err)
	require.NoError(t, store.MarkBroadcast(ctx, b.OperationID, &original.ID))
	candidate := recoveryCandidate(row, b)
	require.NoError(t, facades.Orm().Query().Create(candidate))
	outbox := &callbackOutbox{}
	outbox.afterStage = func(context.Context) error {
		// A separate committed writer changes the pointer after the publisher read
		// the old link. The outbox insert itself holds no withdrawal row lock.
		return store.MarkBroadcast(context.Background(), b.OperationID, &candidate.ID)
	}
	publisher := callbackPublisher(store, outbox)
	err = store.Within(ctx, func(txCtx context.Context) error {
		send, stageErr := publisher.StageFailed(txCtx, original, "old_failure")
		require.Nil(t, send)
		return stageErr
	})
	require.ErrorIs(t, err, models.ErrTransactionSettled)
	saved, err := store.FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, candidate.ID, *saved.TransactionID)
	require.Equal(t, "broadcast", saved.Status)
	require.Nil(t, saved.FailureReason)
	require.Zero(t, callbackEventCount(t))
	require.Zero(t, outbox.sends)
}

func TestRecoveryConfirmedCASRaceDoesNotPublishBeforeGuard(t *testing.T) {
	_, row, b := recoveryRows(t)
	ctx := context.Background()
	store := repositories.NewWithdrawalRepository(nil)
	original, err := repositories.NewTransactionRepository(nil).FindByID(ctx, b.OriginalTransactionID)
	require.NoError(t, err)
	original.Status = string(types.TxStatusConfirmed)
	require.NoError(t, store.MarkBroadcast(ctx, b.OperationID, &original.ID))
	candidate := recoveryCandidate(row, b)
	require.NoError(t, facades.Orm().Query().Create(candidate))
	race := callbackCommittedRace{WithdrawalRepository: store, before: func() { require.NoError(t, store.MarkBroadcast(context.Background(), b.OperationID, &candidate.ID)) }}
	outbox := &callbackOutbox{}
	require.NoError(t, callbackPublisher(race, outbox).MarkConfirmed(ctx, original))
	saved, err := store.FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, candidate.ID, *saved.TransactionID)
	require.Equal(t, "broadcast", saved.Status)
	require.Zero(t, callbackEventCount(t))
	require.Zero(t, outbox.sends)
}

func TestRecoveryConfirmedOutboxFailureRollsBackPhysicalOutcome(t *testing.T) {
	_, _, b := recoveryRows(t)
	ctx := context.Background()
	store := repositories.NewWithdrawalRepository(nil)
	original, err := repositories.NewTransactionRepository(nil).FindByID(ctx, b.OriginalTransactionID)
	require.NoError(t, err)
	original.Status = string(types.TxStatusConfirmed)
	require.NoError(t, store.MarkBroadcast(ctx, b.OperationID, &original.ID))
	outbox := &callbackOutbox{afterStage: func(context.Context) error { return errors.New("outbox failure") }}
	require.ErrorContains(t, callbackPublisher(store, outbox).MarkConfirmed(ctx, original), "outbox failure")
	saved, err := store.FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, "broadcast", saved.Status)
	require.Equal(t, original.ID, *saved.TransactionID)
	require.Zero(t, callbackEventCount(t))
	require.Zero(t, outbox.sends)
}

func TestRecoveryConfirmedOutboxCommitsBeforeQueueAndRefusesOuterTransaction(t *testing.T) {
	_, _, b := recoveryRows(t)
	ctx := context.Background()
	store := repositories.NewWithdrawalRepository(nil)
	original, err := repositories.NewTransactionRepository(nil).FindByID(ctx, b.OriginalTransactionID)
	require.NoError(t, err)
	original.Status = string(types.TxStatusConfirmed)
	require.NoError(t, store.MarkBroadcast(ctx, b.OperationID, &original.ID))
	outbox := &callbackOutbox{atSend: func() {
		visible, lookupErr := repositories.NewWithdrawalRepository(nil).FindByID(context.Background(), b.OperationID)
		require.NoError(t, lookupErr)
		require.Equal(t, "confirmed", visible.Status)
		require.EqualValues(t, 1, callbackEventCount(t))
	}}
	require.NoError(t, callbackPublisher(store, outbox).MarkConfirmed(ctx, original))
	saved, err := store.FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, "confirmed", saved.Status)
	require.EqualValues(t, 1, callbackEventCount(t))
	require.Equal(t, 1, outbox.sends)
	tx, err := facades.Orm().Query().BeginTransaction()
	require.NoError(t, err)
	outer := repositories.NewWithdrawalRepository(tx)
	require.ErrorContains(t, outer.WithinCommitted(ctx, func(context.Context) error { return nil }), "outer transaction")
	require.NoError(t, tx.Rollback())
}

func TestRecoveryCallbacksPreserveLegacyUnlinkedOutcomes(t *testing.T) {
	for _, kind := range []string{"confirmed", "failed"} {
		t.Run(kind, func(t *testing.T) {
			_, _, b := recoveryRows(t)
			ctx := context.Background()
			q := facades.Orm().Query()
			_, err := q.Model(&models.Withdrawal{}).Where("id = ?", b.OperationID).Update("transaction_id", nil)
			require.NoError(t, err)
			store := repositories.NewWithdrawalRepository(nil)
			if kind == "confirmed" {
				require.NoError(t, store.MarkConfirmed(ctx, b.OperationID, b.OriginalTransactionID))
			} else {
				require.NoError(t, store.MarkFailedForTransaction(ctx, b.OperationID, b.OriginalTransactionID, "legacy_failure"))
			}
			saved, err := store.FindByID(ctx, b.OperationID)
			require.NoError(t, err)
			require.Equal(t, kind, saved.Status)
			if kind == "confirmed" {
				require.Equal(t, b.OriginalTransactionID, *saved.TransactionID)
			} else {
				require.Nil(t, saved.TransactionID)
				require.Equal(t, "legacy_failure", *saved.FailureReason)
			}
		})
	}
}

func recoverySendClaimed(t *testing.T) (*repositories.WithdrawalRecoveryRepository, *models.WithdrawalRecoveryAttempt, *models.Transaction) {
	t.Helper()
	recovery, row, binding := recoveryRows(t)
	ctx := context.Background()
	won, err := recovery.Claim(ctx, row)
	require.NoError(t, err)
	require.True(t, won)
	call := `{"nonce":0}`
	won, err = recovery.Transition(ctx, row.ID, models.RecoveryClaimed, models.RecoverySigning, models.WithdrawalRecoveryUpdate{CallJSON: &call})
	require.NoError(t, err)
	require.True(t, won)
	row.State, row.CallJSON = models.RecoverySigning, call
	candidate := recoveryCandidate(row, binding)
	won, err = recovery.RecordCandidate(ctx, row, candidate, "sealed-bytes")
	require.NoError(t, err)
	require.True(t, won)
	won, err = recovery.Transition(ctx, row.ID, models.RecoverySigned, models.RecoverySendClaimed, models.WithdrawalRecoveryUpdate{})
	require.NoError(t, err)
	require.True(t, won)
	return recovery, row, candidate
}

func TestRecoverySendObservationPreservesCommittedCurrentCandidateCallback(t *testing.T) {
	for _, terminal := range []string{models.WithdrawalStatusFailed, models.WithdrawalStatusConfirmed} {
		for _, outcome := range []string{models.RecoveryBroadcast, models.RecoverySendUnknown} {
			t.Run(terminal+"/"+outcome, func(t *testing.T) {
				recovery, row, candidate := recoverySendClaimed(t)
				ctx := context.Background()
				store := repositories.NewWithdrawalRepository(nil)
				outbox := &callbackOutbox{}
				publisher := callbackPublisher(store, outbox)
				candidate.Status = terminal
				terminalPayload := `{"broadcast_outcome":"accepted","sender_nonce":0,"tracker_evidence":"terminal"}`
				_, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", candidate.ID).Update(map[string]any{"status": terminal, "raw_payload": terminalPayload})
				require.NoError(t, err)
				if terminal == models.WithdrawalStatusFailed {
					require.NoError(t, store.WithinCommitted(ctx, func(txCtx context.Context) error {
						_, stageErr := publisher.StageFailed(txCtx, candidate, "candidate_onchain_reverted")
						return stageErr
					}))
				} else {
					require.NoError(t, publisher.MarkConfirmed(ctx, candidate))
				}
				eventsBefore := callbackEventCount(t)
				require.EqualValues(t, 1, eventsBefore)
				won, err := recovery.Transition(ctx, row.ID, models.RecoverySendClaimed, outcome, models.WithdrawalRecoveryUpdate{})
				require.NoError(t, err)
				require.True(t, won)
				withdrawal, err := store.FindByID(ctx, row.OperationID)
				require.NoError(t, err)
				require.Equal(t, terminal, withdrawal.Status)
				require.Equal(t, candidate.ID, *withdrawal.TransactionID)
				if terminal == models.WithdrawalStatusFailed {
					require.NotNil(t, withdrawal.FailureReason)
					require.Equal(t, "candidate_onchain_reverted", *withdrawal.FailureReason)
				} else {
					require.Nil(t, withdrawal.FailureReason)
				}
				persisted, err := repositories.NewTransactionRepository(nil).FindByID(ctx, candidate.ID)
				require.NoError(t, err)
				require.Equal(t, terminal, persisted.Status)
				require.JSONEq(t, terminalPayload, persisted.RawPayload)
				require.Equal(t, eventsBefore, callbackEventCount(t))
				attempt, err := recovery.FindByOperation(ctx, row.OperationID)
				require.NoError(t, err)
				require.Equal(t, outcome, attempt.State)
			})
		}
	}
}

func TestRecoverySendObservationRejectsInconsistentTerminalCandidate(t *testing.T) {
	for _, terminal := range []string{models.WithdrawalStatusFailed, models.WithdrawalStatusConfirmed} {
		t.Run(terminal, func(t *testing.T) {
			recovery, row, candidate := recoverySendClaimed(t)
			ctx := context.Background()
			store := repositories.NewWithdrawalRepository(nil)
			_, err := facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", candidate.ID).Update("status", terminal)
			require.NoError(t, err)
			if terminal == models.WithdrawalStatusFailed {
				require.NoError(t, store.MarkFailedForTransaction(ctx, row.OperationID, candidate.ID, "unverified_failure"))
			} else {
				require.NoError(t, store.MarkConfirmed(ctx, row.OperationID, candidate.ID))
			}
			// Simulate inconsistent transaction evidence without bypassing the
			// aggregate guard or relaxing its terminal-callback validation.
			_, err = facades.Orm().Query().Model(&models.Transaction{}).Where("id = ?", candidate.ID).Update("status", "pending")
			require.NoError(t, err)
			won, err := recovery.Transition(ctx, row.ID, models.RecoverySendClaimed, models.RecoverySendUnknown, models.WithdrawalRecoveryUpdate{})
			require.ErrorContains(t, err, "inconsistent")
			require.False(t, won)
			attempt, err := recovery.FindByOperation(ctx, row.OperationID)
			require.NoError(t, err)
			require.Equal(t, models.RecoverySendClaimed, attempt.State)
			withdrawal, err := store.FindByID(ctx, row.OperationID)
			require.NoError(t, err)
			require.Equal(t, terminal, withdrawal.Status)
			persisted, err := repositories.NewTransactionRepository(nil).FindByID(ctx, candidate.ID)
			require.NoError(t, err)
			require.JSONEq(t, candidate.RawPayload, persisted.RawPayload)
		})
	}
}
