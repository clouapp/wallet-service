package middleware_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// throttleSuite drives the registered limiters through the real router and the
// Redis cache. Each test is its own client: the test peer (192.0.2.1) is made a
// trusted proxy and X-Forwarded-For carries a client address no other test
// uses, so the buckets never meet another test's or another package's.
type throttleSuite struct {
	suite.Suite
	goravelTesting.TestCase

	restore map[string]any
}

func TestThrottle(t *testing.T) {
	suite.Run(t, new(throttleSuite))
}

func (s *throttleSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.restore = map[string]any{}
	s.setConfig("http.trusted_proxies", "192.0.2.1/32")
}

func (s *throttleSuite) TearDownTest() {
	for key, value := range s.restore {
		facades.Config().Add(key, value)
	}
}

func (s *throttleSuite) setConfig(key string, value any) {
	s.T().Helper()
	if _, saved := s.restore[key]; !saved {
		s.restore[key] = facades.Config().Get(key)
	}
	facades.Config().Add(key, value)
}

// uniqueClient is a client address no other test sends from.
func uniqueClient() string {
	return fmt.Sprintf("10.%d.%d.%d", rand.IntN(256), rand.IntN(256), rand.IntN(254)+1)
}

func (s *throttleSuite) post(path, client, body string) contractstestinghttp.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		WithHeader("X-Forwarded-For", client).
		Post(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *throttleSuite) assertThrottled(resp contractstestinghttp.Response) {
	s.T().Helper()
	resp.AssertStatus(429)
	s.NotEmpty(resp.Headers().Get("Retry-After"))
	content, err := resp.Content()
	s.Require().NoError(err)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &body), content)
	s.Equal("too_many_requests", body.Error.Code)
	s.NotEmpty(body.Error.Message)
}

func login(email string) string {
	return fmt.Sprintf(`{"email":%q,"password":"wrong-password"}`, email)
}

func (s *throttleSuite) TestLogin_IsLimited_PerClientAndEmail() {
	s.setConfig("http.throttle.login_per_ip_email", 3)
	s.setConfig("http.throttle.login_per_ip", 0)
	client := uniqueClient()
	email := uuid.NewString() + "@example.com"

	for range 3 {
		s.post("/v1/auth/login", client, login(email)).AssertStatus(401)
	}
	s.assertThrottled(s.post("/v1/auth/login", client, login(email)))

	// The email is part of the key, whatever its case, and so is the client.
	s.assertThrottled(s.post("/v1/auth/login", client, login(strings.ToUpper(email))))
	s.post("/v1/auth/login", client, login(uuid.NewString()+"@example.com")).AssertStatus(401)
	s.post("/v1/auth/login", uniqueClient(), login(email)).AssertStatus(401)
}

func (s *throttleSuite) TestLogin_IsLimited_PerClientAcrossEmails() {
	s.setConfig("http.throttle.login_per_ip_email", 0)
	s.setConfig("http.throttle.login_per_ip", 3)
	client := uniqueClient()

	for range 3 {
		s.post("/v1/auth/login", client, login(uuid.NewString()+"@example.com")).AssertStatus(401)
	}
	s.assertThrottled(s.post("/v1/auth/login", client, login(uuid.NewString()+"@example.com")))
	s.post("/v1/auth/login", uniqueClient(), login(uuid.NewString()+"@example.com")).AssertStatus(401)
}

func (s *throttleSuite) TestZero_TurnsALimitOff() {
	s.setConfig("http.throttle.login_per_ip_email", 0)
	s.setConfig("http.throttle.login_per_ip", 0)
	client := uniqueClient()
	email := uuid.NewString() + "@example.com"

	for range 8 {
		s.post("/v1/auth/login", client, login(email)).AssertStatus(401)
	}
}

func (s *throttleSuite) TestRecover_IsLimited_PerClientAndEmail() {
	s.setConfig("http.throttle.recover_per_ip_email", 2)
	s.setConfig("http.throttle.auth_per_ip", 0)
	client := uniqueClient()
	body := fmt.Sprintf(`{"email":%q}`, uuid.NewString()+"@example.com")

	s.post("/v1/auth/recover", client, body).AssertOk()
	s.post("/v1/auth/recover", client, body).AssertOk()
	s.assertThrottled(s.post("/v1/auth/recover", client, body))
}

func (s *throttleSuite) TestAuthRoutes_AreLimited_PerClientAndPath() {
	s.setConfig("http.throttle.auth_per_ip", 2)
	client := uniqueClient()
	routes := []string{"/v1/auth/2fa/verify", "/v1/auth/refresh", "/v1/auth/register", "/v1/auth/recover/confirm", "/v1/auth/invites/accept"}

	for _, route := range routes {
		s.Run(route, func() {
			for range 2 {
				s.Never429(s.post(route, client, `{}`))
			}
			s.assertThrottled(s.post(route, client, `{}`))
		})
	}
}

// Never429 fails when resp is a rate limit answer.
func (s *throttleSuite) Never429(resp contractstestinghttp.Response) {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(content, "too_many_requests")
}

func (s *throttleSuite) TestAPI_IsLimited_PerToken() {
	s.setConfig("http.throttle.api_per_minute", 2)
	token := "Bearer not-a-token-" + uuid.NewString()
	get := func(bearer string) contractstestinghttp.Response {
		resp, err := s.Http(s.T()).
			WithHeader("Authorization", bearer).
			WithHeader("X-Forwarded-For", uniqueClient()).
			Get("/api/v1/chains")
		s.Require().NoError(err)
		return resp
	}

	get(token).AssertStatus(401)
	get(token).AssertStatus(401)
	s.assertThrottled(get(token))
	// Another token is another bucket, and a forged address does not make a new one.
	get("Bearer not-a-token-" + uuid.NewString()).AssertStatus(401)
}

// gas-check answers one on-chain read per wallet per minute with the body it
// had before the limit moved into middleware.Throttle. The first call's own
// outcome (the wallet's chain is not served here) does not matter: it takes
// the token.
func (s *throttleSuite) TestGasCheck_IsLimited_PerWallet() {
	account := fixtures.InsertAccount(s.T(), "gas-check-"+uuid.NewString())
	wallet := fixtures.InsertWalletWithAccount(s.T(), "nochain", &account.ID)
	other := fixtures.InsertWalletWithAccount(s.T(), "nochain", &account.ID)
	record := &models.AccessToken{ID: uuid.New(), AccountID: account.ID, Name: "gas-" + uuid.NewString()}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, ip_cidr, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, '', ?, NOW(), NOW())`,
		record.ID, record.AccountID, record.Name, "hash-"+record.ID.String(), "{}",
	)
	s.Require().NoError(err)
	token, err := middleware.MintAPIToken(record, false)
	s.Require().NoError(err)

	gasCheck := func(walletID uuid.UUID) contractstestinghttp.Response {
		resp, err := s.Http(s.T()).
			WithHeader("Authorization", "Bearer "+token).
			Post("/api/v1/wallets/"+walletID.String()+"/gas-check", nil)
		s.Require().NoError(err)
		return resp
	}

	first, err := gasCheck(wallet.ID).Content()
	s.Require().NoError(err)
	s.NotContains(first, "rate_limited")

	limited := gasCheck(wallet.ID)
	limited.AssertStatus(429)
	content, err := limited.Content()
	s.Require().NoError(err)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &body), content)
	s.Equal("rate_limited", body.Error.Message)
	s.Contains(content, `"limit_type":"gas_check"`)
	s.Contains(content, `"retry_after_seconds":60`)

	otherBody, err := gasCheck(other.ID).Content()
	s.Require().NoError(err)
	s.NotContains(otherBody, "rate_limited")
}
