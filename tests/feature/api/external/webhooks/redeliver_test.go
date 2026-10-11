package webhooks

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// The suite methods below exercise POST /api/v1/webhooks/{webhookId}/redeliver.

func (s *WebhooksControllerTestSuite) redeliverPath(webhookID string) string {
	return "/api/v1/webhooks/" + webhookID + "/redeliver"
}

// accountWebhook creates a webhook of the token's account through the API.
func (s *WebhooksControllerTestSuite) accountWebhook(token ctltestutil.Token) uuid.UUID {
	resp := s.External("/api/v1/webhooks", token).
		Post(`{"url":"https://example.com/webhook","secret":"redeliver_secret","events":["deposit.confirmed","withdrawal.confirmed"]}`)
	resp.AssertCreated()
	content, err := resp.Content()
	s.Require().NoError(err)
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &created))
	return created.ID
}

// deliveredEvent stores an event of webhookID delivered createdAgo ago.
func (s *WebhooksControllerTestSuite) deliveredEvent(webhookID uuid.UUID, eventType string, createdAgo time.Duration) uuid.UUID {
	id := uuid.New()
	subject := uuid.NewString()
	deliveredAt := time.Now().UTC()
	s.Require().NoError(facades.Orm().Query().Create(&models.WebhookEvent{
		ID:              id,
		WebhookConfigID: &webhookID,
		SubjectID:       &subject,
		EventType:       eventType,
		Payload:         `{"type":"` + eventType + `"}`,
		DeliveryURL:     "https://example.com/webhook",
		DeliveryStatus:  models.WebhookDeliveryDelivered,
		Attempts:        1,
		MaxAttempts:     10,
		DeliveredAt:     &deliveredAt,
	}))
	_, err := facades.Orm().Query().Exec(
		`UPDATE webhook_events SET created_at = (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?), updated_at = (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?) WHERE id = ?`,
		createdAgo.Seconds(), createdAgo.Seconds(), id,
	)
	s.Require().NoError(err)
	return id
}

func (s *WebhooksControllerTestSuite) storedEvent(id uuid.UUID) models.WebhookEvent {
	var event models.WebhookEvent
	s.Require().NoError(facades.Orm().Query().Where("id = ?", id).First(&event))
	return event
}

func (s *WebhooksControllerTestSuite) assertRedelivery(resp interface {
	Content() (string, error)
}, webhookID uuid.UUID, requeued int) {
	content, err := resp.Content()
	s.Require().NoError(err)
	var body map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &body))
	s.Equal(map[string]any{"webhook_id": webhookID.String(), "requeued": float64(requeued)}, body)
}

func (s *WebhooksControllerTestSuite) TestRedeliver_Webhook_RequeuesTheEventsItNames() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	token := ctltestutil.Token{Bearer: bearer}
	webhookID := s.accountWebhook(token)
	eventID := s.deliveredEvent(webhookID, "deposit.confirmed", time.Hour)

	resp := s.External(s.redeliverPath(webhookID.String()), token).Post(`{"event_ids":["` + eventID.String() + `"]}`)
	resp.AssertStatus(http.StatusAccepted)
	s.assertRedelivery(resp, webhookID, 1)

	stored := s.storedEvent(eventID)
	s.Equal(models.WebhookDeliveryPending, stored.DeliveryStatus)
	s.Zero(stored.Attempts)
	s.Nil(stored.DeliveredAt)
}

func (s *WebhooksControllerTestSuite) TestRedeliver_Webhook_RequeuesTheEventsOfAWindowAndType() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	token := ctltestutil.Token{Bearer: bearer}
	webhookID := s.accountWebhook(token)
	deposit := s.deliveredEvent(webhookID, "deposit.confirmed", time.Hour)
	withdrawal := s.deliveredEvent(webhookID, "withdrawal.confirmed", time.Hour)
	since := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)

	resp := s.External(s.redeliverPath(webhookID.String()), token).Post(`{"since":"` + since + `","types":["deposit.confirmed"]}`)
	resp.AssertStatus(http.StatusAccepted)
	s.assertRedelivery(resp, webhookID, 1)
	s.Equal(models.WebhookDeliveryPending, s.storedEvent(deposit).DeliveryStatus)
	s.Equal(models.WebhookDeliveryDelivered, s.storedEvent(withdrawal).DeliveryStatus)
}

func (s *WebhooksControllerTestSuite) TestRedeliver_Webhook_OfAnotherAccountIsNotFound() {
	testutil.SeededTestDB(s.T())
	_, ownerBearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	_, otherBearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	webhookID := s.accountWebhook(ctltestutil.Token{Bearer: ownerBearer})
	eventID := s.deliveredEvent(webhookID, "deposit.confirmed", time.Hour)

	resp := s.External(s.redeliverPath(webhookID.String()), ctltestutil.Token{Bearer: otherBearer}).Post(`{"event_ids":["` + eventID.String() + `"]}`)
	s.AssertError(resp, http.StatusNotFound, "not_found", "webhook not found")
	s.Equal(models.WebhookDeliveryDelivered, s.storedEvent(eventID).DeliveryStatus)
}

func (s *WebhooksControllerTestSuite) TestRedeliver_Webhook_RefusesALegacyWebhookNobodyClaimed() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	legacy := fixtures.InsertWebhookConfig(s.T(), "https://legacy.test/hook", "legacy_secret", []string{"deposit.confirmed"})

	resp := s.External(s.redeliverPath(legacy.ID.String()), ctltestutil.Token{Bearer: bearer}).Post(`{"event_ids":["` + uuid.NewString() + `"]}`)
	s.AssertError(resp, http.StatusForbidden, "forbidden", "claim the webhook with its secret before asking for a redelivery")
}

func (s *WebhooksControllerTestSuite) TestRedeliver_Webhook_RefusesAnInactiveWebhook() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	token := ctltestutil.Token{Bearer: bearer}
	webhookID := s.accountWebhook(token)
	s.External("/api/v1/webhooks/"+webhookID.String(), token).Patch(`{"is_active":false}`).AssertOk()

	resp := s.External(s.redeliverPath(webhookID.String()), token).Post(`{"event_ids":["` + uuid.NewString() + `"]}`)
	s.AssertError(resp, http.StatusConflict, "conflict", "webhook is inactive")
}

func (s *WebhooksControllerTestSuite) TestRedeliver_Webhook_RefusesAnEmptyOrAmbiguousSelection() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	token := ctltestutil.Token{Bearer: bearer}
	webhookID := s.accountWebhook(token)
	path := s.redeliverPath(webhookID.String())

	for _, body := range []string{
		`{}`,
		`{"event_ids":["` + uuid.NewString() + `"],"since":"2026-10-10T00:00:00Z"}`,
		`{"event_ids":["not-a-uuid"]}`,
		`{"since":"yesterday"}`,
	} {
		resp := s.External(path, token).Post(body)
		s.AssertError(resp, http.StatusUnprocessableEntity, "validation_failed", "validation failed")
	}
}

func (s *WebhooksControllerTestSuite) TestRedeliver_Webhook_RefusesASinceOlderThanSevenDays() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	token := ctltestutil.Token{Bearer: bearer}
	webhookID := s.accountWebhook(token)
	since := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339)

	resp := s.External(s.redeliverPath(webhookID.String()), token).Post(`{"since":"` + since + `"}`)
	s.AssertError(resp, http.StatusUnprocessableEntity, "validation_failed", "validation failed")
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"since":["since must be within the last 7 days and before until"]`)
}

func (s *WebhooksControllerTestSuite) TestRedeliver_Webhook_RefusesAnIDThatIsNotAUUID() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := s.External(s.redeliverPath("not-a-uuid"), ctltestutil.Token{Bearer: bearer}).Post(`{"event_ids":["` + uuid.NewString() + `"]}`)
	s.AssertError(resp, http.StatusBadRequest, "invalid_request", "invalid webhook id")
}
