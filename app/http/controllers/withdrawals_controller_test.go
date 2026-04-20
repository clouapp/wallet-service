package controllers_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/mocks"
)

// WithdrawalsControllerTestSuite is the residual cover for withdrawal-adjacent
// middleware behaviour on the external API. The happy-path and signed/unsigned
// token coverage lives in critical_api_endpoints_test.go (TestCriticalEndpointsSuite),
// so this file only keeps the IDOR/404 assertion on /api/v1/wallets/{id}/withdraw/preview.
//
// The four HMAC-signed legacy tests that used to live here
// (TestCreateWithdrawal_Success, _Idempotency, _InvalidAddress,
// _MissingIdempotencyKey) were deleted: they targeted a hypothetical
// request/response shape (external_user_id, to_address, asset,
// idempotency_key, transaction_id, tx_hash, origin) that never matched the
// production controller contract, so they could not exercise real code paths.
type WithdrawalsControllerTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWithdrawalsControllerSuite(t *testing.T) {
	suite.Run(t, new(WithdrawalsControllerTestSuite))
}

func (s *WithdrawalsControllerTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

// TestCreateWithdrawal_WalletNotFound asserts that hitting a withdrawal-adjacent
// external API endpoint with a wallet UUID the caller does not own (or that
// does not exist) returns the generic 404 "wallet not found" body produced by
// the APIWalletContext middleware. The route is a valid POST under the
// /api/v1/wallets/{walletId} group so the middleware runs before any
// controller, matching the v2 auth model introduced in commit a7ddf1a.
func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_WalletNotFound() {
	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "acc-withdraw-not-found",
		Status:      "active",
		Environment: "prod",
	}))

	tokenID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		tokenID, accountID, "withdraw-not-found-token", "test-hash-withdraw-not-found", "{}",
	)
	s.Require().NoError(err)

	jwt, err := middleware.MintAPIToken(&models.AccessToken{
		ID:        tokenID,
		AccountID: accountID,
		Name:      "withdraw-not-found-token",
	}, false)
	s.Require().NoError(err)

	body := `{"external_user_id":"user","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"100","asset":"eth"}`
	unknownWallet := uuid.NewString()
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("Content-Type", "application/json").
		Post("/api/v1/wallets/"+unknownWallet+"/withdraw/preview", strings.NewReader(body))
	s.Require().NoError(err)

	resp.AssertStatus(404).AssertJson(map[string]any{"error": "wallet not found"})
}
