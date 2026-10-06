package repositories_test

import (
	"context"
	"testing"

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
	s.NoError(err)

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
