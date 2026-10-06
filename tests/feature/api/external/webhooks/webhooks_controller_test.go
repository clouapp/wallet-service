package webhooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/services/settings"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// WebhooksControllerTestSuite exercises the external /api/v1/webhooks
// endpoints (CreateWebhook, ListWebhooks). Per-wallet webhook management is
// a dashboard-only concern and is covered elsewhere.
type WebhooksControllerTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWebhooks_Controller_Suite(t *testing.T) {
	suite.Run(t, new(WebhooksControllerTestSuite))
}

func (s *WebhooksControllerTestSuite) TestCreate_Webhook_Success() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	const secret = "webhook_secret_123"
	body := `{"url":"https://example.com/webhook","secret":"` + secret + `","events":["deposit.confirmed","withdrawal.confirmed"]}`
	resp := ctltestutil.Post(s.T(), &s.TestCase, "/api/v1/webhooks", body, bearer, nil)
	resp.AssertCreated().AssertJson(map[string]any{
		"url":       "https://example.com/webhook",
		"is_active": true,
	})

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.NotEmpty(payload["id"])
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
	ctltestutil.
		Post(s.T(), &s.TestCase, "/api/v1/webhooks", body, bearer, nil).
		AssertStatus(422)
}

// TestCreateWebhook_MissingSecret — validator requires secret; returns 422.
func (s *WebhooksControllerTestSuite) TestCreate_Webhook_MissingSecret() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"url":"https://example.com/webhook","events":["deposit.confirmed"]}`
	ctltestutil.
		Post(s.T(), &s.TestCase, "/api/v1/webhooks", body, bearer, nil).
		AssertStatus(422)
}

// TestCreateWebhook_MissingEvents — validator requires events; returns 422.
func (s *WebhooksControllerTestSuite) TestCreate_Webhook_MissingEvents() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"url":"https://example.com/webhook","secret":"webhook_secret"}`
	ctltestutil.
		Post(s.T(), &s.TestCase, "/api/v1/webhooks", body, bearer, nil).
		AssertStatus(422)
}

func (s *WebhooksControllerTestSuite) TestList_Webhooks_Empty() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/webhooks", bearer)
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
	ctltestutil.
		Post(s.T(), &s.TestCase, "/api/v1/webhooks", create, bearer, nil).
		AssertCreated()

	resp := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/webhooks", bearer)
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Len(payload.Data, 1)
	s.Equal("https://a.com/webhook", payload.Data[0]["url"])
	s.Equal(true, payload.Data[0]["is_active"])
	_, hasSecret := payload.Data[0]["secret"]
	s.False(hasSecret, "secret MUST NOT appear in list payload")
}

func (s *WebhooksControllerTestSuite) TestList_Webhooks_Multiple() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	ctltestutil.
		Post(s.T(), &s.TestCase, "/api/v1/webhooks",
			`{"url":"https://a.com/webhook","secret":"secret_a","events":["deposit.confirmed"]}`,
			bearer, nil).
		AssertCreated()
	ctltestutil.
		Post(s.T(), &s.TestCase, "/api/v1/webhooks",
			`{"url":"https://b.com/webhook","secret":"secret_b","events":["withdrawal.confirmed"]}`,
			bearer, nil).
		AssertCreated()

	resp := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/webhooks", bearer)
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
