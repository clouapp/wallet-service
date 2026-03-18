package controllers_test

import (
	"testing"

	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"github.com/macromarkets/vault/tests/mocks"
)

type WebhooksControllerTestSuite struct {
	authSuite
}

func TestWebhooksControllerSuite(t *testing.T) {
	suite.Run(t, new(WebhooksControllerTestSuite))
}

func (s *WebhooksControllerTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *WebhooksControllerTestSuite) TestCreateWebhook_Success() {
	s.SignedPost("/v1/webhooks",
		`{"url":"https://example.com/webhook","secret":"webhook_secret_123","events":["deposit.confirmed","withdrawal.confirmed"]}`).
		AssertCreated().
		AssertJson(map[string]any{"url": "https://example.com/webhook", "is_active": true}).
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.Has("id").Has("events").Missing("secret")
		})
}

func (s *WebhooksControllerTestSuite) TestCreateWebhook_MissingURL() {
	s.SignedPost("/v1/webhooks", `{"secret":"webhook_secret","events":["deposit.confirmed"]}`).AssertBadRequest()
}

func (s *WebhooksControllerTestSuite) TestCreateWebhook_MissingSecret() {
	s.SignedPost("/v1/webhooks", `{"url":"https://example.com/webhook","events":["deposit.confirmed"]}`).AssertBadRequest()
}

func (s *WebhooksControllerTestSuite) TestCreateWebhook_MissingEvents() {
	s.SignedPost("/v1/webhooks", `{"url":"https://example.com/webhook","secret":"webhook_secret"}`).AssertBadRequest()
}

func (s *WebhooksControllerTestSuite) TestCreateWebhook_EmptyEventsArray() {
	s.SignedPost("/v1/webhooks", `{"url":"https://example.com/webhook","secret":"webhook_secret","events":[]}`).AssertBadRequest()
}

func (s *WebhooksControllerTestSuite) TestListWebhooks_Empty() {
	s.SignedGet("/v1/webhooks").
		AssertOk().AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
		json.Has("data").Count("data", 0)
	})
}

func (s *WebhooksControllerTestSuite) TestListWebhooks_WithData() {
	s.SignedPost("/v1/webhooks", `{"url":"https://a.com/webhook","secret":"secret_a","events":["deposit.confirmed"]}`)

	s.SignedGet("/v1/webhooks").
		AssertOk().
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.HasWithScope("data", 1, func(j contractstestinghttp.AssertableJSON) {
				j.Where("url", "https://a.com/webhook").
					Where("is_active", true).
					Has("events").
					Missing("secret")
			})
		})
}

func (s *WebhooksControllerTestSuite) TestListWebhooks_Multiple() {
	s.SignedPost("/v1/webhooks", `{"url":"https://a.com/webhook","secret":"secret_a","events":["deposit.confirmed"]}`)
	s.SignedPost("/v1/webhooks", `{"url":"https://b.com/webhook","secret":"secret_b","events":["withdrawal.confirmed"]}`)

	s.SignedGet("/v1/webhooks").
		AssertOk().
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.Count("data", 2).
				Each("data", func(j contractstestinghttp.AssertableJSON) {
					j.Has("id").Has("url").Has("events").Has("is_active").Missing("secret")
				})
		})
}
