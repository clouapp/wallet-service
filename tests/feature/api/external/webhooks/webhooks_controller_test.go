package webhooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/services/settings"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// WebhooksControllerTestSuite exercises the external /api/v1/webhooks
// endpoints (CreateWebhook, ListWebhooks). Per-wallet webhook management is
// a dashboard-only concern and is covered elsewhere.
type WebhooksControllerTestSuite struct {
	ctltestutil.HTTPSuite
}

func TestWebhooks_Controller_Suite(t *testing.T) {
	ctltestutil.RunSuite(t, new(WebhooksControllerTestSuite))
}

func (s *WebhooksControllerTestSuite) TestCreate_Webhook_Success() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	const secret = "webhook_secret_123"
	body := `{"url":"https://example.com/webhook","secret":"` + secret + `","events":["deposit.confirmed","withdrawal.confirmed"]}`
	resp := s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Post(body)
	resp.AssertCreated().AssertJson(map[string]any{
		"url":       "https://example.com/webhook",
		"is_active": true,
	})

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Require().NotEmpty(payload["id"])
	s.NotNil(payload["events"])
	_, hasSecret := payload["secret"]
	s.False(hasSecret, "secret MUST NOT appear in webhook response body")

	var stored string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT secret FROM webhook_configs WHERE id = ?`, payload["id"],
	).Scan(&stored))
	if !settings.IsSealed(stored) || stored == secret {
		s.T().Fatal("created webhook secret is not sealed")
	}
	if strings.Contains(content, secret) || strings.Contains(content, stored) {
		s.T().Fatal("webhook HTTP JSON contains the signing secret")
	}
}

// TestCreateWebhook_MissingURL — validator requires url; returns 422.
func (s *WebhooksControllerTestSuite) TestCreate_Webhook_MissingURL() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"secret":"webhook_secret","events":["deposit.confirmed"]}`
	resp := s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Post(body)
	s.AssertError(resp, 422, "validation_failed", "validation failed")
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, "url is required to not be empty")
}

// TestCreateWebhook_MissingSecret — validator requires secret; returns 422.
func (s *WebhooksControllerTestSuite) TestCreate_Webhook_MissingSecret() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"url":"https://example.com/webhook","events":["deposit.confirmed"]}`
	resp := s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Post(body)
	s.AssertError(resp, 422, "validation_failed", "validation failed")
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, "secret is required to not be empty")
}

// TestCreateWebhook_MissingEvents — validator requires events; returns 422.
func (s *WebhooksControllerTestSuite) TestCreate_Webhook_MissingEvents() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"url":"https://example.com/webhook","secret":"webhook_secret"}`
	resp := s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Post(body)
	s.AssertError(resp, 422, "validation_failed", "validation failed")
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, "events is required to not be empty")
}

func (s *WebhooksControllerTestSuite) TestList_Webhooks_Empty() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Empty(payload.Data)
}

func (s *WebhooksControllerTestSuite) TestList_Webhooks_WithData() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	create := `{"url":"https://a.com/webhook","secret":"secret_a","events":["deposit.confirmed"]}`
	s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Post(create).
		AssertCreated()

	resp := s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Require().Len(payload.Data, 1)
	s.Equal("https://a.com/webhook", payload.Data[0]["url"])
	s.Equal(true, payload.Data[0]["is_active"])
	_, hasSecret := payload.Data[0]["secret"]
	s.False(hasSecret, "secret MUST NOT appear in list payload")
}

func (s *WebhooksControllerTestSuite) TestList_Webhooks_Multiple() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Post(`{"url":"https://a.com/webhook","secret":"secret_a","events":["deposit.confirmed"]}`).
		AssertCreated()
	s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Post(`{"url":"https://b.com/webhook","secret":"secret_b","events":["withdrawal.confirmed"]}`).
		AssertCreated()

	resp := s.External("/api/v1/webhooks", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Len(payload.Data, 2)
	for _, w := range payload.Data {
		s.NotEmpty(w["id"])
		s.NotEmpty(w["url"])
		s.NotNil(w["events"])
		s.Equal(true, w["is_active"])
		_, hasSecret := w["secret"]
		s.False(hasSecret, "secret MUST NOT appear in list payload")
	}
}
