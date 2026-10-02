package controllers_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/mocks"
)

// criticalEndpointsSuite exercises the high-value external API endpoints
// (GenerateAddress, Consolidate, and — when registered — CreateWithdrawal)
// under both the legacy unsigned Bearer JWT scheme and the HMAC-required
// signed scheme introduced by the `require_signature` claim.
//
// All requests hit the production Goravel router via s.Http(s.T()). Each
// test seeds its own account + wallet + access_token row so tests are
// isolated from one another.
//
// Wallets are seeded directly via the ORM with real secp256k1 MPC material
// (the private key is generated in-test and discarded — it never signs a
// transaction). That path keeps the tests independent of LocalStack,
// chain adapter registration, and the chains-table seed fixtures, which
// the per-package TestMain does not install.
type criticalEndpointsSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestCriticalEndpointsSuite(t *testing.T) {
	suite.Run(t, new(criticalEndpointsSuite))
}

func (s *criticalEndpointsSuite) SetupTest() {
	mocks.TestDB(s.T())
}

// seedAccountWallet creates an Account, an access_tokens row, mints a JWT
// (with the requested require_signature claim) and seeds a secp256k1 MPC
// wallet bound to the account. Returns the wallet UUID (as a string for
// URL interpolation) and the raw bearer JWT.
//
// The access_tokens table requires a token_hash column that the ORM model
// doesn't expose, so we fall back to raw SQL — matching the pattern used
// in middleware-level tests.
func (s *criticalEndpointsSuite) seedAccountWallet(requireSignature bool, label string) (string, string) {
	s.T().Helper()

	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "critical-" + label,
		Status:      "active",
		Environment: "prod",
	}))

	tokenID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, permissions, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		tokenID, accountID, "critical-token-"+label, "test-hash-critical-"+label, models.AllAPIPermissionGrants(), "{}",
	)
	s.Require().NoError(err)

	jwt, err := middleware.MintAPIToken(&models.AccessToken{
		ID:        tokenID,
		AccountID: accountID,
		Name:      "critical-token-" + label,
	}, requireSignature)
	s.Require().NoError(err)

	priv, err := btcec.NewPrivateKey()
	s.Require().NoError(err)
	pubCompressed := priv.PubKey().SerializeCompressed()
	chainCode := sha256.Sum256([]byte("critical-endpoints-chain-code-" + label))

	walletID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Wallet{
		ID:               walletID,
		Chain:            models.ChainETH,
		Label:            "critical-endpoints wallet",
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     hex.EncodeToString(pubCompressed),
		MPCChainCode:     hex.EncodeToString(chainCode[:]),
		MPCCurve:         "secp256k1",
		AccountID:        &accountID,
	}))

	return walletID.String(), jwt
}

// mintSignedToken mints a JWT without seeding a wallet. Used by the
// "missing signature" negative cases, which are rejected by the
// APITokenAuth middleware before any wallet lookup happens.
func (s *criticalEndpointsSuite) mintSignedToken(label string) string {
	s.T().Helper()

	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "critical-" + label,
		Status:      "active",
		Environment: "prod",
	}))

	tokenID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, permissions, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		tokenID, accountID, "critical-token-"+label, "test-hash-critical-"+label, models.AllAPIPermissionGrants(), "{}",
	)
	s.Require().NoError(err)

	jwt, err := middleware.MintAPIToken(&models.AccessToken{
		ID:        tokenID,
		AccountID: accountID,
		Name:      "critical-token-" + label,
	}, true)
	s.Require().NoError(err)
	return jwt
}

// signBody returns the hex-encoded HMAC-SHA256 of `body` keyed by the raw
// JWT. This matches the scheme enforced by APITokenAuth when the token's
// `require_signature` claim is true.
func signBody(jwt, body string) string {
	mac := hmac.New(sha256.New, []byte(jwt))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// post dispatches a JSON POST against the production Goravel router and
// returns the response. When signature is non-empty the X-Signature
// header is attached.
func (s *criticalEndpointsSuite) post(path, jwt, body, signature string) contractstestinghttp.Response {
	s.T().Helper()

	req := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("Content-Type", "application/json")
	if signature != "" {
		req = req.WithHeader("X-Signature", signature)
	}
	resp, err := req.Post(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

// assertNoMiddlewareReject fails the test if the response body carries any
// of the middleware-level reject strings. Used on "AcceptsRequest" cases
// where the controller may still return 4xx/5xx due to unmocked chain
// infrastructure, but we need to confirm auth + HMAC verification + the
// per-wallet ownership check all passed.
func (s *criticalEndpointsSuite) assertNoMiddlewareReject(resp contractstestinghttp.Response) {
	s.T().Helper()
	body, err := resp.Content()
	s.Require().NoError(err)

	rejects := []string{
		`"error":"missing bearer token"`,
		`"error":"invalid or expired api token"`,
		`"error":"token not found or revoked"`,
		`"error":"missing request signature"`,
		`"error":"invalid request signature"`,
		`"error":"wallet not found"`,
	}
	for _, rej := range rejects {
		s.NotContains(body, rej,
			"response body should not contain middleware reject %q — got: %s", rej, body)
	}
}

// ---------------------------------------------------------------------------
// GenerateAddress — POST /api/v1/wallets/{walletId}/addresses
// ---------------------------------------------------------------------------

func (s *criticalEndpointsSuite) TestGenerateAddress_UnsignedToken_OK() {
	walletID, jwt := s.seedAccountWallet(false, "gen-addr-unsigned")

	body := `{"external_user_id":"user_unsigned","label":"test-addr"}`
	s.post("/api/v1/wallets/"+walletID+"/addresses", jwt, body, "").
		AssertCreated().
		AssertJson(map[string]any{
			"external_user_id": "user_unsigned",
			"chain":            "eth",
			"wallet_id":        walletID,
		})
}

func (s *criticalEndpointsSuite) TestGenerateAddress_SignedToken_OK() {
	walletID, jwt := s.seedAccountWallet(true, "gen-addr-signed")

	body := `{"external_user_id":"user_signed","label":"test-addr"}`
	sig := signBody(jwt, body)
	s.post("/api/v1/wallets/"+walletID+"/addresses", jwt, body, sig).
		AssertCreated().
		AssertJson(map[string]any{
			"external_user_id": "user_signed",
			"chain":            "eth",
			"wallet_id":        walletID,
		})
}

// TestGenerateAddress_SignedTokenMissingSig_401 verifies the middleware
// rejects a signed-required token that carries no X-Signature header.
// No wallet seeding is needed because the middleware aborts before the
// APIWalletContext runs.
func (s *criticalEndpointsSuite) TestGenerateAddress_SignedTokenMissingSig_401() {
	jwt := s.mintSignedToken("gen-addr-missing-sig")

	body := `{"external_user_id":"user_missing_sig"}`
	s.post("/api/v1/wallets/"+uuid.NewString()+"/addresses", jwt, body, "").
		AssertStatus(401).
		AssertJson(map[string]any{"error": "missing request signature"})
}

// ---------------------------------------------------------------------------
// ConsolidateWallet — POST /api/v1/wallets/{walletId}/consolidate
//
// The "AcceptsRequest" tests only assert that the middleware stack let the
// request through — downstream consolidation will fail (insufficient
// funds, chain adapter missing, or MPC sign error) because no on-chain
// infrastructure is wired up. The important assertion is that none of the
// middleware reject bodies come back: auth, HMAC verification, and the
// per-wallet ownership check all passed.
// ---------------------------------------------------------------------------

func (s *criticalEndpointsSuite) TestConsolidate_UnsignedToken_AcceptsRequest() {
	walletID, jwt := s.seedAccountWallet(false, "consolidate-unsigned")

	body := `{"asset":"eth","passphrase":"test-pass-phrase-12345"}`
	resp := s.post("/api/v1/wallets/"+walletID+"/consolidate", jwt, body, "")
	s.assertNoMiddlewareReject(resp)
}

func (s *criticalEndpointsSuite) TestConsolidate_SignedToken_AcceptsRequest() {
	walletID, jwt := s.seedAccountWallet(true, "consolidate-signed")

	body := `{"asset":"eth","passphrase":"test-pass-phrase-12345"}`
	sig := signBody(jwt, body)
	resp := s.post("/api/v1/wallets/"+walletID+"/consolidate", jwt, body, sig)
	s.assertNoMiddlewareReject(resp)
}

func (s *criticalEndpointsSuite) TestConsolidate_SignedTokenMissingSig_401() {
	jwt := s.mintSignedToken("consolidate-missing-sig")

	body := `{"asset":"eth","passphrase":"test-pass-phrase-12345"}`
	s.post("/api/v1/wallets/"+uuid.NewString()+"/consolidate", jwt, body, "").
		AssertStatus(401).
		AssertJson(map[string]any{"error": "missing request signature"})
}

// ---------------------------------------------------------------------------
// CreateWalletWithdrawal — POST /api/v1/wallets/{walletId}/withdrawals
//
// The "AcceptsRequest" tests only assert that the middleware stack let the
// request through — the downstream service will fail at the MPC
// passphrase-decryption step (the test wallet carries dummy MPC material
// so DecryptShareA cannot succeed), which the controller maps to 401
// "invalid passphrase". That error body does not match any middleware
// reject string, so `assertNoMiddlewareReject` correctly passes.
// ---------------------------------------------------------------------------

// critWithdrawalBody is the shared JSON payload exercised by the
// withdrawal accept-path tests. It satisfies the external-API rule set:
// amount, destination_address, and a ≥12-char passphrase. No totp_code
// is sent — APITokenAuth is the authentication factor on /api/v1/*, and
// Rules() conditionally drops totp_code when user_id is absent.
const critWithdrawalBody = `{"amount":"1","destination_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","passphrase":"test-passphrase-123"}`

func (s *criticalEndpointsSuite) TestCreateWithdrawal_UnsignedToken_AcceptsRequest() {
	walletID, jwt := s.seedAccountWallet(false, "withdrawal-unsigned")

	resp := s.post("/api/v1/wallets/"+walletID+"/withdrawals", jwt, critWithdrawalBody, "")
	s.assertNoMiddlewareReject(resp)
}

func (s *criticalEndpointsSuite) TestCreateWithdrawal_SignedToken_AcceptsRequest() {
	walletID, jwt := s.seedAccountWallet(true, "withdrawal-signed")

	sig := signBody(jwt, critWithdrawalBody)
	resp := s.post("/api/v1/wallets/"+walletID+"/withdrawals", jwt, critWithdrawalBody, sig)
	s.assertNoMiddlewareReject(resp)
}

func (s *criticalEndpointsSuite) TestCreateWithdrawal_SignedTokenMissingSig_401() {
	jwt := s.mintSignedToken("withdrawal-missing-sig")

	s.post("/api/v1/wallets/"+uuid.NewString()+"/withdrawals", jwt, critWithdrawalBody, "").
		AssertStatus(401).
		AssertJson(map[string]any{"error": "missing request signature"})
}
