package middleware_test

import (
	"os"
	"testing"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/macrowallets/waas/tests/testenv"
)

// testJWTSecret must be set before Goravel boots so that MintAPIToken / APITokenAuth
// use the same signing key.
const testJWTSecret = "test-jwt-secret-for-middleware-tests"

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	_ = os.Setenv("JWT_SECRET", testJWTSecret)
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		_ = os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}
	bootstrap.Boot()
	_ = container.Get()
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// APIWalletContext integration tests
//
// Verify that /api/v1/wallets/{walletId}/* endpoints enforce per-wallet
// ownership against the API token's account. Each test seeds two accounts
// with distinct wallets and access tokens, then mints real JWTs and hits the
// production routes.
// ---------------------------------------------------------------------------

type APIWalletContextTestSuite struct {
	suite.Suite
	goravelTesting.TestCase

	accountA models.Account
	accountB models.Account
	walletA  models.Wallet
	tokenA   string // JWT signed for accountA
	tokenB   string // JWT signed for accountB
}

func TestAPIWalletContextSuite(t *testing.T) {
	suite.Run(t, new(APIWalletContextTestSuite))
}

func (s *APIWalletContextTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.seedFixtures()
}

// seedFixtures creates two accounts, one wallet owned by account A, and one
// access token per account. Tokens are minted via middleware.MintAPIToken so
// they are valid Bearer credentials for APITokenAuth.
func (s *APIWalletContextTestSuite) seedFixtures() {
	s.accountA = models.Account{
		ID:          uuid.New(),
		Name:        "Account A",
		Status:      "active",
		Environment: "prod",
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.accountA))

	s.accountB = models.Account{
		ID:          uuid.New(),
		Name:        "Account B",
		Status:      "active",
		Environment: "prod",
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.accountB))

	// Wallet has a circular FK with addresses (wallet.deposit_address_id ↔
	// addresses.wallet_id), so we create the wallet first without a deposit
	// address, then the address, then backfill wallet.deposit_address_id.
	s.walletA = models.Wallet{
		ID:               uuid.New(),
		Chain:            "eth",
		Label:            "Account A wallet",
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     "02abc123def456",
		MPCCurve:         "secp256k1",
		AccountID:        &s.accountA.ID,
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.walletA))

	addrID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Address{
		ID:              addrID,
		WalletID:        s.walletA.ID,
		Chain:           "eth",
		Address:         "0x" + uuid.NewString()[:16],
		DerivationIndex: 0,
		ExternalUserID:  "system",
		IsActive:        true,
		Label:           "Deposit",
	}))
	_, err := facades.Orm().Query().Model(&models.Wallet{}).Where("id = ?", s.walletA.ID).Update("deposit_address_id", addrID)
	s.Require().NoError(err)
	s.walletA.DepositAddressID = &addrID

	s.tokenA = s.createAccessTokenJWT(s.accountA.ID, "token-a")
	s.tokenB = s.createAccessTokenJWT(s.accountB.ID, "token-b")
}

// createAccessTokenJWT inserts an access_tokens row via raw SQL (the model
// doesn't expose the token_hash column that the schema requires) and returns
// a Bearer JWT that APITokenAuth will accept.
func (s *APIWalletContextTestSuite) createAccessTokenJWT(accountID uuid.UUID, name string) string {
	record := &models.AccessToken{
		ID:        uuid.New(),
		AccountID: accountID,
		Name:      name,
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		record.ID, record.AccountID, record.Name, "test-hash-"+name, "{}",
	)
	s.Require().NoError(err)

	jwt, err := middleware.MintAPIToken(record, false)
	s.Require().NoError(err)
	return jwt
}

func (s *APIWalletContextTestSuite) authedGet(path, jwt string) contractstestinghttp.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		Get(path)
	s.Require().NoError(err)
	return resp
}

// TestWalletBelongsToAnotherAccount is the IDOR case: account B tries to
// access account A's wallet. The middleware must return 404 with a generic
// message — never 403 or anything that leaks the wallet's existence.
func (s *APIWalletContextTestSuite) TestWalletBelongsToAnotherAccount() {
	resp := s.authedGet("/api/v1/wallets/"+s.walletA.ID.String()+"/gas-status", s.tokenB)
	resp.AssertStatus(404).AssertJson(map[string]any{"error": map[string]any{
		"code":    "not_found",
		"message": "wallet not found",
	}})
}

// TestWalletNotFound returns 404 with the same body as the cross-account
// case, so callers cannot distinguish "exists but not yours" from "does not
// exist at all".
func (s *APIWalletContextTestSuite) TestWalletNotFound() {
	missing := uuid.New().String()
	resp := s.authedGet("/api/v1/wallets/"+missing+"/gas-status", s.tokenA)
	resp.AssertStatus(404).AssertJson(map[string]any{"error": map[string]any{
		"code":    "not_found",
		"message": "wallet not found",
	}})
}

// TestValidOwnership confirms the middleware allows the request through when
// the token's account owns the wallet. We don't assert 200 because the
// downstream sweep service depends on chain adapters that aren't wired in
// tests — we only need to prove the middleware did not short-circuit with a
// 404 `wallet not found`.
func (s *APIWalletContextTestSuite) TestValidOwnership() {
	resp := s.authedGet("/api/v1/wallets/"+s.walletA.ID.String()+"/gas-status", s.tokenA)
	body, err := resp.Content()
	s.Require().NoError(err)
	s.T().Logf("valid ownership response: %s", body)
	// The middleware must let the request reach the controller, so we must
	// NOT see the middleware's 404 body.
	s.NotContains(body, `"message":"wallet not found"`)
}

// TestInvalidWalletIDFormat is a sanity case: non-UUID walletId must 404
// through the middleware, not bubble up as a 500.
func (s *APIWalletContextTestSuite) TestInvalidWalletIDFormat() {
	resp := s.authedGet("/api/v1/wallets/not-a-uuid/gas-status", s.tokenA)
	resp.AssertStatus(404).AssertJson(map[string]any{"error": map[string]any{
		"code":    "not_found",
		"message": "wallet not found",
	}})
}
