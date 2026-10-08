package middleware_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
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

func TestAPI_Token_AuthHMACSuite(t *testing.T) {
	suite.Run(t, new(APITokenAuthHMACTestSuite))
}

func (s *APITokenAuthHMACTestSuite) SetupTest() {
	fixtures.TestDB(s.T())

	s.account = models.Account{
		ID:          uuid.New(),
		Name:        "hmac-test-account",
		Status:      "active",
		Environment: "prod",
	}
	s.Require().NoError(facades.Orm().Query().Create(&s.account))
}

// mintToken inserts an access_tokens row whose token_hash is the pre-secret
// stored form (not a sha256 digest) and returns a signed JWT with the given
// require_signature claim and no secret claim.
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
	s.NotContains(body, `"message":"missing request signature"`)
	s.NotContains(body, `"message":"invalid request signature"`)
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

	resp.AssertStatus(401).AssertJson(map[string]any{"error": map[string]any{
		"code":    "invalid_signature",
		"message": "missing request signature",
	}})
}

// TestAPITokenAuth_Invalid401: any token that presents an X-Signature header
// must be verified. A wrong signature is always a 401 "invalid request
// signature", regardless of the require_signature claim value.
func (s *APITokenAuthHMACTestSuite) TestAPI_TokenAuth_Invalid401() {
	jwt := s.mintToken(true, "external-invalid-sig")

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("X-Signature", "deadbeefdeadbeef").
		Get("/api/v1/chains")
	s.Require().NoError(err)

	resp.AssertStatus(401).AssertJson(map[string]any{"error": map[string]any{
		"code":    "invalid_signature",
		"message": "invalid request signature",
	}})
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

// The tests below pin the X-Signature scheme as it is today (v1), so the v2
// scheme in docs/api-request-signing.md can be added next to it without a
// client that works now breaking. v1: the key is the whole bearer JWT, the
// message is the body only, the value is lowercase hex of HMAC-SHA256, and a
// signature that is present is checked even when the token does not require one.

func (s *APITokenAuthHMACTestSuite) signatureRejected(resp contractstestinghttp.Response) {
	s.T().Helper()
	resp.AssertStatus(401).AssertJson(map[string]any{"error": map[string]any{
		"code":    "invalid_signature",
		"message": "invalid request signature",
	}})
}

// An optional signature is still verified: a token that does not require one
// is refused when it sends a wrong one.
func (s *APITokenAuthHMACTestSuite) TestV1_WrongSignature_IsRefused_EvenWhenTheTokenDoesNotRequireOne() {
	jwt := s.mintToken(false, "v1-optional-wrong")

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("X-Signature", hmacHex(jwt, "something else")).
		Get("/api/v1/chains")
	s.Require().NoError(err)

	s.signatureRejected(resp)
}

func (s *APITokenAuthHMACTestSuite) TestV1_RightSignature_IsAccepted_WhenTheTokenDoesNotRequireOne() {
	jwt := s.mintToken(false, "v1-optional-right")

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("X-Signature", hmacHex(jwt, "")).
		Get("/api/v1/chains")
	s.Require().NoError(err)

	resp.AssertStatus(200)
}

// The message is the body alone: a GET signs the empty string, so its
// signature is the same on every path and query.
func (s *APITokenAuthHMACTestSuite) TestV1_AGet_SignsTheEmptyBody_WhateverThePathOrQuery() {
	jwt := s.mintToken(true, "v1-get")
	sig := hmacHex(jwt, "")

	for _, path := range []string{"/api/v1/chains", "/api/v1/wallets", "/api/v1/wallets?limit=1"} {
		resp, err := s.Http(s.T()).
			WithHeader("Authorization", "Bearer "+jwt).
			WithHeader("X-Signature", sig).
			Get(path)
		s.Require().NoError(err)
		content, err := resp.Content()
		s.Require().NoError(err)
		s.assertNotSignatureReject(content)
	}
}

// The compare is case-sensitive on the hex digits.
func (s *APITokenAuthHMACTestSuite) TestV1_UppercaseHex_IsRefused() {
	jwt := s.mintToken(true, "v1-uppercase")
	sig := strings.ToUpper(hmacHex(jwt, ""))
	s.Require().NotEqual(hmacHex(jwt, ""), sig)

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("X-Signature", sig).
		Get("/api/v1/chains")
	s.Require().NoError(err)

	s.signatureRejected(resp)
}

// The key is the bearer JWT: a signature made with any other key is refused.
func (s *APITokenAuthHMACTestSuite) TestV1_SignatureMadeWithAnotherKey_IsRefused() {
	jwt := s.mintToken(true, "v1-other-key")

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		WithHeader("X-Signature", hmacHex(jwt+"x", "")).
		Get("/api/v1/chains")
	s.Require().NoError(err)

	s.signatureRejected(resp)
}

// Verifying the signature reads the body; the handler must still see all of it.
// An empty body is a 400, so a signed request answering the same as an unsigned
// one proves the body was handed back.
func (s *APITokenAuthHMACTestSuite) TestV1_TheHandlerStillSeesTheWholeBodyAfterVerification() {
	jwt := s.mintToken(false, "v1-body")
	body := `{"url":"not-a-url"}`

	post := func(headers map[string]string) string {
		request := s.Http(s.T()).
			WithHeader("Authorization", "Bearer "+jwt).
			WithHeader("Content-Type", "application/json")
		for name, value := range headers {
			request = request.WithHeader(name, value)
		}
		resp, err := request.Post("/api/v1/webhooks", strings.NewReader(body))
		s.Require().NoError(err)
		content, err := resp.Content()
		s.Require().NoError(err)
		return content
	}

	unsignedBody := post(nil)
	signedBody := post(map[string]string{"X-Signature": hmacHex(jwt, body)})

	s.Equal(unsignedBody, signedBody)
	s.NotContains(signedBody, "invalid request body")
	s.assertNotSignatureReject(signedBody)
}

// A v2-format value (docs/api-request-signing.md) is not a valid v1 value, so
// today it is a 401 for every token. That is what lets the server accept the
// format later, by dispatching on it, without changing the answer for any
// client that works now.
func (s *APITokenAuthHMACTestSuite) TestV1_AV2FormatHeader_IsRefused_Today() {
	for _, requireSignature := range []bool{false, true} {
		jwt := s.mintToken(requireSignature, fmt.Sprintf("v1-v2-format-%t", requireSignature))
		value := "t=1760000000,n=00112233445566778899aabbccddeeff,v2=" + hmacHex(jwt, "")

		resp, err := s.Http(s.T()).
			WithHeader("Authorization", "Bearer "+jwt).
			WithHeader("X-Signature", value).
			Get("/api/v1/chains")
		s.Require().NoError(err)

		s.signatureRejected(resp)
	}
}

func (s *APITokenAuthHMACTestSuite) TestSuccessful_Call_StampsLastUsedWithoutChangingBody() {
	jwt := s.mintToken(false, "usage-stamp")

	first, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		Get("/api/v1/chains")
	s.Require().NoError(err)
	first.AssertStatus(200)
	firstBody, err := first.Content()
	s.Require().NoError(err)

	second, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		Get("/api/v1/chains")
	s.Require().NoError(err)
	second.AssertStatus(200)
	secondBody, err := second.Content()
	s.Require().NoError(err)

	s.Equal(firstBody, secondBody)
	s.NotContains(firstBody, "last_used_at")

	var stored models.AccessToken
	s.Require().NoError(facades.Orm().Query().Where("name = ?", "usage-stamp").First(&stored))
	s.NotNil(stored.LastUsedAt)
	s.Nil(stored.RevokedAt)
}

func (s *APITokenAuthHMACTestSuite) TestRevoked_Token_IsUnauthorized() {
	jwt := s.mintToken(false, "revoked-stamp")
	_, err := facades.Orm().Query().Exec(
		`UPDATE access_tokens SET revoked_at = NOW() WHERE account_id = ? AND name = ?`,
		s.account.ID, "revoked-stamp",
	)
	s.Require().NoError(err)

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+jwt).
		Get("/api/v1/chains")
	s.Require().NoError(err)
	resp.AssertStatus(401).AssertJson(map[string]any{"error": map[string]any{
		"code":    "unauthorized",
		"message": "token not found or revoked",
	}})

	var stored models.AccessToken
	s.Require().NoError(facades.Orm().Query().Where("name = ?", "revoked-stamp").First(&stored))
	s.Nil(stored.LastUsedAt)
	s.NotNil(stored.RevokedAt)
}

// TestAPITokenAuth_LegacyStoredHashStillAuthenticates: a row written as the
// previous hash-of-the-id form, and a JWT with no secret claim, still passes.
func (s *APITokenAuthHMACTestSuite) TestAPI_TokenAuth_LegacyStoredHashStillAuthenticates() {
	record := &models.AccessToken{
		ID:        uuid.New(),
		AccountID: s.account.ID,
		Name:      "legacy-id-hash",
	}
	stored := authsvc.NewService().HashToken(record.ID.String())
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		record.ID, record.AccountID, record.Name, stored, "{}",
	)
	s.Require().NoError(err)
	signed, err := middleware.MintAPIToken(record, false)
	s.Require().NoError(err)

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+signed).
		Get("/api/v1/chains")
	s.Require().NoError(err)
	resp.AssertStatus(200)
}

// TestAPITokenAuth_SecretDigestRejectsAMissingClaim: a sha256 row is not the
// legacy form. A JWT without the secret claim is rejected.
func (s *APITokenAuthHMACTestSuite) TestAPI_TokenAuth_SecretDigestRejectsAMissingClaim() {
	record := &models.AccessToken{
		ID:        uuid.New(),
		AccountID: s.account.ID,
		Name:      "digest-without-claim",
	}
	passwords := authsvc.NewService()
	secret, err := passwords.GenerateAPITokenSecret()
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		record.ID, record.AccountID, record.Name, passwords.HashAPITokenSecret(secret), "{}",
	)
	s.Require().NoError(err)
	signed, err := middleware.MintAPIToken(record, false)
	s.Require().NoError(err)

	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+signed).
		Get("/api/v1/chains")
	s.Require().NoError(err)
	resp.AssertStatus(401).AssertJson(map[string]any{"error": map[string]any{
		"code":    "unauthorized",
		"message": "invalid or expired api token",
	}})
}
