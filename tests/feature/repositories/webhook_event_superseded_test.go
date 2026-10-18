package repositories_test

import (
	"context"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// depositEvent stores an undelivered eventType row of transaction txID.
func (s *WebhookEventRepositoryTestSuite) depositEvent(eventType string, txID *uuid.UUID) models.WebhookEvent {
	event := &models.WebhookEvent{
		ID: uuid.New(), TransactionID: txID, EventType: eventType, Payload: `{}`,
		DeliveryURL: "https://example.com/hook", DeliveryStatus: models.WebhookDeliveryPending, MaxAttempts: 10,
	}
	s.Require().NoError(s.repo.Create(context.Background(), event))
	return *event
}

func (s *WebhookEventRepositoryTestSuite) superseded(event models.WebhookEvent) bool {
	got, err := s.repo.SupersededByDepositFailure(context.Background(), event.ID.String())
	s.Require().NoError(err)
	return got
}

func (s *WebhookEventRepositoryTestSuite) TestSuperseded_ByDepositFailure_HoldsTheProgressEventsOfAFailedDeposit() {
	wallet := fixtures.InsertWallet(s.T(), models.ChainTETH)
	confirming := fixtures.InsertTransaction(s.T(), wallet.ID, nil, models.ChainTETH, models.TxTypeDeposit, "confirming", models.NativeETH, "1", 100)
	confirmed := fixtures.InsertTransaction(s.T(), wallet.ID, nil, models.ChainTETH, models.TxTypeDeposit, "confirmed", models.NativeETH, "1", 100)
	pending := s.depositEvent("deposit.pending", &confirming.ID)
	progress := s.depositEvent("deposit.confirming", &confirming.ID)
	stale := s.depositEvent("deposit.confirmed", &confirmed.ID)
	for _, event := range []models.WebhookEvent{pending, progress, stale} {
		s.False(s.superseded(event), "%s of a deposit that has not failed is delivered", event.EventType)
	}

	// The tracker publishes deposit.failed before it marks the row failed.
	failedEvent := s.depositEvent("deposit.failed", &confirming.ID)
	s.True(s.superseded(pending), "deposit.pending after deposit.failed was published")
	s.True(s.superseded(progress), "deposit.confirming after deposit.failed was published")
	s.False(s.superseded(failedEvent), "deposit.failed itself is delivered")

	// A deposit marked failed holds its events even for a config that never subscribed to deposit.failed.
	_, err := facades.Orm().Query().Exec(`UPDATE transactions SET status = 'failed' WHERE id = ?`, confirmed.ID)
	s.Require().NoError(err)
	s.True(s.superseded(stale), "deposit.confirmed of a deposit that failed")
}

func (s *WebhookEventRepositoryTestSuite) TestSuperseded_ByDepositFailure_LeavesEveryOtherEvent() {
	wallet := fixtures.InsertWallet(s.T(), models.ChainTETH)
	withdrawal := fixtures.InsertTransaction(s.T(), wallet.ID, nil, models.ChainTETH, models.TxTypeWithdrawal, "failed", models.NativeETH, "1", 100)
	failedWithdrawal := s.depositEvent("withdrawal.confirmed", &withdrawal.ID)
	withoutTransaction := s.depositEvent("deposit.confirmed", nil)

	s.False(s.superseded(failedWithdrawal), "only deposit progress events are held")
	s.False(s.superseded(withoutTransaction), "an event without a transaction is not held")
	got, err := s.repo.SupersededByDepositFailure(context.Background(), uuid.NewString())
	s.Require().NoError(err)
	s.False(got, "an unknown event is not held")
	_, err = s.repo.SupersededByDepositFailure(context.Background(), " ")
	s.Error(err, "an event id is required")
}
