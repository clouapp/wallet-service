package controllers_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/mocks"
)

type WithdrawalsControllerTestSuite struct {
	authSuite
}

func TestWithdrawalsControllerSuite(t *testing.T) {
	suite.Run(t, new(WithdrawalsControllerTestSuite))
}

func (s *WithdrawalsControllerTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *WithdrawalsControllerTestSuite) createWallet() string {
	resp := s.SignedPost("/v1/wallets", `{"chain":"eth"}`)
	j, _ := resp.Json()
	return j["id"].(string)
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_Success() {
	walletID := s.createWallet()
	resp := s.SignedPost("/v1/wallets/"+walletID+"/withdrawals",
		`{"external_user_id":"user_withdraw","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"1000000","asset":"eth","idempotency_key":"withdraw_001"}`).
		AssertCreated()

	j, _ := resp.Json()
	s.NotEmpty(j["transaction_id"], "transaction_id should be present")
	s.Contains(j, "tx_hash")
	s.Contains(j, "status")
	s.Contains(j, "origin")
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_Idempotency() {
	walletID := s.createWallet()
	body := `{"external_user_id":"user_idem","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"500000","asset":"eth","idempotency_key":"idem_test_001"}`

	j1, _ := s.SignedPost("/v1/wallets/"+walletID+"/withdrawals", body).Json()
	j2, _ := s.SignedPost("/v1/wallets/"+walletID+"/withdrawals", body).Json()

	s.NotEmpty(j1["transaction_id"], "transaction_id should be present in first response")
	s.Equal(j1["transaction_id"], j2["transaction_id"])
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_MissingIdempotencyKey() {
	walletID := s.createWallet()
	s.SignedPost("/v1/wallets/"+walletID+"/withdrawals",
		`{"external_user_id":"user","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"100","asset":"eth"}`).
		AssertBadRequest()
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_InvalidAddress() {
	walletID := s.createWallet()
	s.SignedPost("/v1/wallets/"+walletID+"/withdrawals",
		`{"external_user_id":"user","to_address":"invalid_address","amount":"100","asset":"eth","idempotency_key":"invalid_addr_001"}`).
		AssertStatus(409)
}

// TestCreateWithdrawal_WalletNotFound asserts that hitting a withdrawal-adjacent
// external API endpoint with a wallet UUID the caller does not own (or that
// does not exist) returns the generic 404 "wallet not found" body produced by
// the APIWalletContext middleware. The route is a valid POST under the
// /api/v1/wallets/{walletId} group so the middleware runs before any
// controller, matching the v2 auth model introduced in commit a7ddf1a.
//
// The legacy `/v1/wallets/.../withdrawals` path this test used to exercise is
// now guarded by SessionAuth (dashboard) and cannot be driven with HMAC
// headers; mint a real Bearer JWT and target the external API instead.
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
	})
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
