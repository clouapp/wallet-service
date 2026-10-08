package middleware_test

import (
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

// apiTokenIPSuite checks S3.4.6 on /api/v1. A blank ip_cidr keeps today's
// access. A set allowlist refuses any other address with 403 forbidden.
// The suite never prints the minted token.
type apiTokenIPSuite struct {
	suite.Suite
	goravelTesting.TestCase

	account models.Account
}

func TestAPI_Token_IP(t *testing.T) {
	suite.Run(t, new(apiTokenIPSuite))
}

func (s *apiTokenIPSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.account = models.Account{
		ID:          uuid.New(),
		Name:        "api-ip-" + uuid.NewString(),
		Status:      "active",
		Environment: "prod",
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.account))
}

func (s *apiTokenIPSuite) TestBlank_Allowlist_KeepsTodaysAccess() {
	token := s.mint("")
	s.get(token).AssertOk()
}

func (s *apiTokenIPSuite) TestAllowlist_That_ContainsTheClientIsOk() {
	token := s.mint("192.0.2.0/24")
	s.get(token).AssertOk()
}

func (s *apiTokenIPSuite) TestAllowlist_Refuses_AnotherIP() {
	token := s.mint("198.51.100.0/24")
	s.get(token).AssertForbidden().AssertJson(map[string]any{
		"error": map[string]any{"code": "forbidden", "message": "forbidden"},
	})
}

func (s *apiTokenIPSuite) TestBare_Address_AllowsOnlyThatAddress() {
	token := s.mint("192.0.2.1")
	s.get(token).AssertOk()
}

func (s *apiTokenIPSuite) mint(ipCidr string) string {
	s.T().Helper()
	record := &models.AccessToken{
		ID:        uuid.New(),
		AccountID: s.account.ID,
		Name:      "ip-" + uuid.NewString(),
		IpCidr:    ipCidr,
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, ip_cidr, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		record.ID, record.AccountID, record.Name, "hash-"+record.ID.String(), ipCidr, "{}",
	)
	s.Require().NoError(err)
	signed, err := middleware.MintAPIToken(record, false)
	s.Require().NoError(err)
	return signed
}

func (s *apiTokenIPSuite) get(token string) contractstestinghttp.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).WithHeader("Authorization", "Bearer "+token).Get("/api/v1/chains")
	s.Require().NoError(err)
	return resp
}
