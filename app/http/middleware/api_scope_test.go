package middleware_test

import (
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

// apiScopeSuite checks S3.4.6 on the external routes. A blank permissions
// store keeps today's access. A JSON array is limited to the names it lists.
// The suite never prints the minted token.
type apiScopeSuite struct {
	suite.Suite
	goravelTesting.TestCase

	account models.Account
}

func TestAPIScope(t *testing.T) {
	suite.Run(t, new(apiScopeSuite))
}

func (s *apiScopeSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.account = models.Account{
		ID:          uuid.New(),
		Name:        "api-scope-" + uuid.NewString(),
		Status:      "active",
		Environment: "prod",
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.account))
}

func (s *apiScopeSuite) TestBlankPermissionsKeepTodaysAccess() {
	token := s.mint("")

	s.get(token, "/api/v1/wallets").AssertOk()
	s.get(token, "/api/v1/transactions").AssertOk()
	s.get(token, "/api/v1/webhooks").AssertOk()
}

func (s *apiScopeSuite) TestListedPermissionsAreTheOnlyAccess() {
	token := s.mint(`["transactions.read","webhooks.read"]`)

	s.get(token, "/api/v1/wallets").AssertForbidden().AssertJson(map[string]any{
		"error": map[string]any{"code": "forbidden", "message": "forbidden"},
	})
	s.get(token, "/api/v1/transactions").AssertOk()
	s.get(token, "/api/v1/webhooks").AssertOk()
	s.get(token, "/api/v1/chains").AssertOk()
}

func (s *apiScopeSuite) TestCreateWalletRequiresWalletsCreate() {
	reader := s.mint(`["wallets.read"]`)
	s.post(reader, "/api/v1/wallets").AssertForbidden().AssertJson(map[string]any{
		"error": map[string]any{"code": "forbidden", "message": "forbidden"},
	})

	creator := s.mint(`["wallets.create"]`)
	s.post(creator, "/api/v1/wallets").AssertUnprocessableEntity()
}

func (s *apiScopeSuite) TestMissingWalletIs404BeforeTheScopeCheck() {
	token := s.mint(`["transactions.read"]`)
	s.get(token, "/api/v1/wallets/"+uuid.NewString()).AssertNotFound()
}

func (s *apiScopeSuite) TestMissingTransactionAndWebhookAre404BeforeTheScopeCheck() {
	denied := s.mint(`["wallets.read"]`)
	allowedTx := s.mint(`["transactions.read"]`)
	allowedHook := s.mint(`["webhooks.write"]`)
	missing := uuid.NewString()

	for _, token := range []string{denied, allowedTx} {
		resp := s.get(token, "/api/v1/transactions/"+missing)
		resp.AssertNotFound().AssertJson(map[string]any{
			"error": map[string]any{"code": "not_found", "message": "transaction not found"},
		})
	}
	s.get(denied, "/api/v1/transactions/not-a-uuid").AssertBadRequest()

	for _, token := range []string{denied, allowedHook} {
		resp := s.patch(token, "/api/v1/webhooks/"+missing)
		resp.AssertNotFound().AssertJson(map[string]any{
			"error": map[string]any{"code": "not_found", "message": "webhook not found"},
		})
	}
	s.patch(denied, "/api/v1/webhooks/not-a-uuid").AssertBadRequest()
}

func (s *apiScopeSuite) mint(permissions string) string {
	s.T().Helper()
	record := &models.AccessToken{
		ID:          uuid.New(),
		AccountID:   s.account.ID,
		Name:        "scope-" + uuid.NewString(),
		Permissions: permissions,
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, permissions, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NULLIF(btrim(CAST(? AS text)), '')::jsonb, ?, NOW(), NOW())`,
		record.ID, record.AccountID, record.Name, "hash-"+record.ID.String(), permissions, "{}",
	)
	s.Require().NoError(err)
	signed, err := middleware.MintAPIToken(record, false)
	s.Require().NoError(err)
	return signed
}

func (s *apiScopeSuite) get(token, path string) contractstestinghttp.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).WithHeader("Authorization", "Bearer "+token).Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *apiScopeSuite) patch(token, path string) contractstestinghttp.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Patch(path, strings.NewReader(`{"is_active":true}`))
	s.Require().NoError(err)
	return resp
}

func (s *apiScopeSuite) post(token, path string) contractstestinghttp.Response {
	s.T().Helper()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		Post(path, strings.NewReader(`{}`))
	s.Require().NoError(err)
	return resp
}
