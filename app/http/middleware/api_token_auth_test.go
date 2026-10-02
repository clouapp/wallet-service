package middleware_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

// ---------------------------------------------------------------------------
// APITokenAuth HMAC enforcement tests
//
// These exercise the JWT `require_signature` (claim name "sig") behaviour on
// the production /api/v1 routes. We mint real JWTs via MintAPIToken and hit
// /api/v1/chains (a simple GET guarded only by APITokenAuth). We verify only
// middleware-level outcomes; we never assert on downstream controller bodies
// beyond checking that the middleware did NOT short-circuit with one of its
// signature-related 401 responses.
// ---------------------------------------------------------------------------

type APITokenAuthHMACTestSuite struct {
	suite.Suite
	goravelTesting.TestCase

	account models.Account
}

func TestAPITokenAuthHMACSuite(t *testing.T) {
	suite.Run(t, new(APITokenAuthHMACTestSuite))
}

func (s *APITokenAuthHMACTestSuite) SetupTest() {
	mocks.TestDB(s.T())

	s.account = models.Account{
		ID:          uuid.New(),
		Name:        "hmac-test-account",
		Status:      "active",
		Environment: "prod",
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.account))
}

// mintToken inserts an access_tokens row (token_hash is required by the
// schema but unused by APITokenAuth) and returns a signed JWT with the
// given require_signature claim.
func (s *APITokenAuthHMACTestSuite) mintToken(requireSignature bool, name string) string {
	record := &models.AccessToken{
		ID:        uuid.New(),
		AccountID: s.account.ID,
		Name:      name,
	}
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, permissions, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		record.ID, record.AccountID, record.Name, "test-hash-"+name, models.AllAPIPermissionGrants(), "{}",
	)
	s.Require().NoError(err)

	jwt, err := middleware.MintAPIToken(record, requireSignature)
	s.Require().NoError(err)
	return jwt
}

func hmacHex(key, body string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// assertNotSignatureReject confirms the middleware did NOT abort with one of
// its signature-related 401 bodies. Whatever the downstream controller
// returns is acceptable — the middleware is what we're testing.
func (s *APITokenAuthHMACTestSuite) assertNotSignatureReject(body string) {
	s.NotContains(body, `"error":"missing request signature"`)
	s.NotContains(body, `"error":"invalid request signature"`)
}

// TestAPITokenAuth_NoSignatureOK_WhenClaimFalse: legacy/internal tokens
// (require_signature=false) must still authenticate without an HMAC header.
func (s *APITokenAuthHMACTestSuite) TestAPITokenAuth_NoSignatureOK_WhenClaimFalse() {
	jwt := s.mintToken(false, "legacy-token")

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		Get("/api/v1/chains")
	s.Require().NoError(err)

	body, err := resp.Content()
	s.Require().NoError(err)
	s.assertNotSignatureReject(body)
}

// TestAPITokenAuth_Missing401_WhenClaimTrue: tokens minted with
// require_signature=true MUST be rejected with 401 "missing request
// signature" when the caller omits X-Signature.
func (s *APITokenAuthHMACTestSuite) TestAPITokenAuth_Missing401_WhenClaimTrue() {
	jwt := s.mintToken(true, "external-missing")

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		Get("/api/v1/chains")
	s.Require().NoError(err)

	resp.AssertStatus(401).AssertJson(map[string]any{"error": "missing request signature"})
}

// TestAPITokenAuth_Invalid401: any token that presents an X-Signature header
// must be verified. A wrong signature is always a 401 "invalid request
// signature", regardless of the require_signature claim value.
func (s *APITokenAuthHMACTestSuite) TestAPITokenAuth_Invalid401() {
	jwt := s.mintToken(true, "external-invalid-sig")

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("X-Signature", "deadbeefdeadbeef").
		Get("/api/v1/chains")
	s.Require().NoError(err)

	resp.AssertStatus(401).AssertJson(map[string]any{"error": "invalid request signature"})
}

// TestAPITokenAuth_ValidOK_WhenClaimTrue: a token with require_signature=true
// presenting a correct HMAC of the request body (computed with the raw JWT
// as the key) must pass the middleware. We POST "{}" so the HMAC covers a
// real body.
func (s *APITokenAuthHMACTestSuite) TestAPITokenAuth_ValidOK_WhenClaimTrue() {
	jwt := s.mintToken(true, "external-valid")
	body := "{}"
	sig := hmacHex(jwt, body)

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("X-Signature", sig).
		WithHeader("Content-Type", "application/json").
		Post("/api/v1/wallets", strings.NewReader(body))
	s.Require().NoError(err)

	content, err := resp.Content()
	s.Require().NoError(err)
	s.assertNotSignatureReject(content)
}
