package machine

import (
	"encoding/json"
	"strings"
	"testing"

	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// TenantProvisioningTestSuite is POST /internal/v1/tenants: the provisioning key
// guards it, the body is a form request, and a retry finds what the first call made.
type TenantProvisioningTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestTenant_ProvisioningSuite(t *testing.T) {
	suite.Run(t, new(TenantProvisioningTestSuite))
}

func (s *TenantProvisioningTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *TenantProvisioningTestSuite) post(key, body string) contractstestinghttp.Response {
	s.T().Helper()
	request := s.Http(s.T()).WithHeader("Content-Type", "application/json").WithHeader("X-Forwarded-For", fixtures.ClientIP())
	if key != "" {
		request = request.WithHeader(middleware.TenantProvisioningKeyHeader, key)
	}
	resp, err := request.Post("/internal/v1/tenants", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *TenantProvisioningTestSuite) TestA_ARefusedKeyOrBodyCreatesNothing() {
	body := `{"external_id":"tenant-a","name":"Tenant A","environment":"test"}`
	s.post("", body).AssertStatus(401)
	s.post(testProvisioningKey+"x", body).AssertStatus(401)
	s.post(testProvisioningKey, `{"external_id":"tenant-a","name":"Tenant A","environment":"staging"}`).AssertStatus(422)
	s.post(testProvisioningKey, `{"name":"Tenant A","environment":"test"}`).AssertStatus(422)
}

func (s *TenantProvisioningTestSuite) TestB_ProvisionsOnceAndARetryFindsTheSameAccount() {
	body := `{"external_id":"tenant-b","name":"Tenant B","environment":"prod"}`
	created := s.post(testProvisioningKey, body)
	created.AssertStatus(201)
	first := decode(s.T(), created)
	s.NotEmpty(first["account_id"])
	s.NotEmpty(first["api_token"])
	s.Equal("prod", first["environment"])

	again := s.post(testProvisioningKey, body)
	again.AssertStatus(200)
	second := decode(s.T(), again)
	s.Equal(first["account_id"], second["account_id"], "a retry resumes the same account")
}

func decode(t *testing.T, resp contractstestinghttp.Response) map[string]any {
	t.Helper()
	content, err := resp.Content()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		t.Fatalf("decode %q: %v", content, err)
	}
	return out
}
