package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type WebhookEventRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.WebhookEventRepository
}

func TestWebhook_Event_RepositorySuite(t *testing.T) {
	suite.Run(t, new(WebhookEventRepositoryTestSuite))
}

func (s *WebhookEventRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewWebhookEventRepository(nil)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_Create_Success() {
	event := &models.WebhookEvent{
		ID:             uuid.New(),
		EventType:      "deposit.confirmed",
		Payload:        `{"test": true}`,
		DeliveryURL:    "https://example.com/hook",
		DeliveryStatus: "pending",
		Attempts:       0,
		MaxAttempts:    10,
	}
	err := s.repo.Create(context.Background(), event)
	s.NoError(err)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_Mark_Delivered() {
	event := &models.WebhookEvent{
		ID:             uuid.New(),
		EventType:      "deposit.confirmed",
		Payload:        `{"test": true}`,
		DeliveryURL:    "https://example.com/hook",
		DeliveryStatus: "pending",
		Attempts:       0,
		MaxAttempts:    10,
	}
	s.Require().NoError(s.repo.Create(context.Background(), event))

	err := s.repo.MarkDelivered(context.Background(), event.ID.String())
	s.Require().NoError(err)

	var check models.WebhookEvent
	facades.Orm().Query().Where("id = ?", event.ID).First(&check)
	s.Equal("delivered", check.DeliveryStatus)
	s.NotNil(check.DeliveredAt)
	s.Equal(1, check.Attempts)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_Increment_Attempt() {
	event := &models.WebhookEvent{
		ID:             uuid.New(),
		EventType:      "deposit.confirmed",
		Payload:        `{"test": true}`,
		DeliveryURL:    "https://example.com/hook",
		DeliveryStatus: "pending",
		Attempts:       0,
		MaxAttempts:    10,
	}
	s.Require().NoError(s.repo.Create(context.Background(), event))

	_ = s.repo.IncrementAttempt(context.Background(), event.ID.String(), "HTTP 500")
	_ = s.repo.IncrementAttempt(context.Background(), event.ID.String(), "HTTP 502")

	var check models.WebhookEvent
	facades.Orm().Query().Where("id = ?", event.ID).First(&check)
	s.Equal(2, check.Attempts)
	s.Equal("HTTP 502", check.LastError)
}

// insertOutboxRow stores one event for configID (nil leaves the column empty)
// and moves its timestamps back by createdAgo and updatedAgo.
func (s *WebhookEventRepositoryTestSuite) insertOutboxRow(configID *uuid.UUID, status string, attempts int, createdAgo, updatedAgo time.Duration) models.WebhookEvent {
	event := &models.WebhookEvent{
		ID:              uuid.New(),
		WebhookConfigID: configID,
		EventType:       "deposit.confirmed",
		Payload:         `{"test": true}`,
		DeliveryURL:     "https://example.com/hook",
		DeliveryStatus:  status,
		Attempts:        attempts,
		MaxAttempts:     3,
	}
	s.Require().NoError(s.repo.Create(context.Background(), event))
	_, err := facades.Orm().Query().Exec(
		`UPDATE webhook_events SET created_at = (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?), updated_at = (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?) WHERE id = ?`,
		createdAgo.Seconds(), updatedAgo.Seconds(), event.ID,
	)
	s.Require().NoError(err)
	return *event
}

func (s *WebhookEventRepositoryTestSuite) storedOutboxRow(id uuid.UUID) models.WebhookEvent {
	var row models.WebhookEvent
	s.Require().NoError(facades.Orm().Query().Where("id = ?", id).First(&row))
	return row
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_ClaimStaleForRedispatch_TakesOnlyLostPendingRows() {
	ctx := context.Background()
	active := fixtures.InsertWebhookConfig(s.T(), "https://active.test/hook", "secret", []string{"deposit.confirmed"})
	inactive := fixtures.InsertWebhookConfig(s.T(), "https://inactive.test/hook", "secret", []string{"deposit.confirmed"})
	_, err := facades.Orm().Query().Exec(`UPDATE webhook_configs SET is_active = false WHERE id = ?`, inactive.ID)
	s.Require().NoError(err)
	stale := 5 * time.Minute

	lost := s.insertOutboxRow(&active.ID, models.WebhookDeliveryPending, 0, 10*time.Minute, 10*time.Minute)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryPending, 0, time.Minute, time.Minute)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryPending, 1, time.Hour, time.Minute)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryDelivered, 1, time.Hour, time.Hour)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryFailed, 1, time.Hour, time.Hour)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryPending, 3, time.Hour, time.Hour)
	s.insertOutboxRow(&inactive.ID, models.WebhookDeliveryPending, 0, time.Hour, time.Hour)
	s.insertOutboxRow(nil, models.WebhookDeliveryPending, 0, time.Hour, time.Hour)

	claimed, err := s.repo.ClaimStaleForRedispatch(ctx, stale, 10)
	s.Require().NoError(err)
	s.Require().Len(claimed, 1)
	s.Equal(lost.ID, claimed[0].ID)
	s.Equal(1, claimed[0].Attempts, "a re-dispatch counts as one attempt")
	s.Equal(lost.Payload, claimed[0].Payload)
	s.Require().NotNil(claimed[0].WebhookConfigID)
	s.Equal(active.ID, *claimed[0].WebhookConfigID)

	stored := s.storedOutboxRow(lost.ID)
	s.Equal(1, stored.Attempts)
	s.Equal(models.WebhookDeliveryPending, stored.DeliveryStatus)
	s.Require().NotNil(stored.UpdatedAt)
	s.WithinDuration(time.Now().UTC(), stored.UpdatedAt.StdTime(), time.Minute, "the claim touches the row")

	again, err := s.repo.ClaimStaleForRedispatch(ctx, stale, 10)
	s.Require().NoError(err)
	s.Empty(again, "a claimed row waits another stale period")
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_ClaimStaleForRedispatch_TakesTheOldestUpToTheLimit() {
	ctx := context.Background()
	cfg := fixtures.InsertWebhookConfig(s.T(), "https://active.test/hook", "secret", []string{"deposit.confirmed"})
	oldest := s.insertOutboxRow(&cfg.ID, models.WebhookDeliveryPending, 0, 3*time.Hour, 3*time.Hour)
	middle := s.insertOutboxRow(&cfg.ID, models.WebhookDeliveryPending, 0, 2*time.Hour, 2*time.Hour)
	newest := s.insertOutboxRow(&cfg.ID, models.WebhookDeliveryPending, 0, time.Hour, time.Hour)

	claimed, err := s.repo.ClaimStaleForRedispatch(ctx, 5*time.Minute, 2)
	s.Require().NoError(err)
	ids := map[uuid.UUID]bool{}
	for _, row := range claimed {
		ids[row.ID] = true
	}
	s.Len(claimed, 2)
	s.True(ids[oldest.ID] && ids[middle.ID], "the oldest rows go first")
	s.False(ids[newest.ID])
	s.Equal(0, s.storedOutboxRow(newest.ID).Attempts)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_ClaimStaleForRedispatch_RefusesABadWindow() {
	_, err := s.repo.ClaimStaleForRedispatch(context.Background(), 0, 10)
	s.Error(err)
	_, err = s.repo.ClaimStaleForRedispatch(context.Background(), time.Minute, 0)
	s.Error(err)
}
