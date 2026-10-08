package auth

import (
	"encoding/json"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// AuthControllerTestSuite exercises the /v1/auth/* pre-authentication routes.
// These endpoints do NOT require a bearer token — they are the surface a
// caller hits before having a session — so we dispatch plain HTTP requests
// and assert on validation and credential-rejection behaviour.
//
// Nothing here calls /api/v1, so SetupAPIAuth is not needed. The pre-auth
// paths intentionally skip both SessionAuth and APITokenAuth middleware.
type AuthControllerTestSuite struct {
	support.HTTPSuite
}

func TestAuth_Controller_Suite(t *testing.T) {
	support.RunSuite(t, new(AuthControllerTestSuite))
}

// TestRegister_MissingBody returns 400 when no JSON body is provided.
func (s *AuthControllerTestSuite) TestRegister_Missing_Body() {
	resp := s.Post("/v1/auth/register", support.Session{}, nil)
	s.AssertError(resp, 400, "invalid_request", "invalid request body")
}

// TestRegister_MissingEmail returns 422 when email is absent (validation errors).
func (s *AuthControllerTestSuite) TestRegister_Missing_Email() {
	body := `{"password":"secret123"}`
	resp := s.Post("/v1/auth/register", support.Session{}, body)
	s.AssertError(resp, 422, "validation_failed", "validation failed")
	var parsed struct {
		Errors map[string][]string `json:"errors"`
	}
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.NotEmpty(parsed.Errors["email"])
	s.Equal([]string{"Organization name is required"}, parsed.Errors["organization_name"])
}

// TestLogin_InvalidCredentials returns 401 for an unknown email.
func (s *AuthControllerTestSuite) TestLogin_Invalid_Credentials() {
	body := `{"email":"nonexistent@example.com","password":"wrongpass"}`
	resp := s.Post("/v1/auth/login", support.Session{}, body)
	s.AssertError(resp, 401, "unauthorized", "invalid credentials")
}

// TestLogin_MalformedJSON answers 400 with the same envelope as an empty body.
// The gin driver logs the quoted body of such a request; hiding it from the log
// (app/services/security.RedactText) must not change this answer.
func (s *AuthControllerTestSuite) TestLogin_Malformed_JSON() {
	resp := s.Post("/v1/auth/login", support.Session{}, `{"email":"admin@macro.markets","password":"secre`)
	s.AssertError(resp, 400, "invalid_request", "invalid request body")
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Equal(`{"error":{"code":"invalid_request","message":"invalid request body"}}`, content)
}

// TestLogin_UnknownEmail_SpendsABcryptCompare keeps response time from telling a
// registered email from an unknown one: both run one bcrypt compare.
func (s *AuthControllerTestSuite) TestLogin_Unknown_Email_SpendsABcryptCompare() {
	start := time.Now()
	_ = bcrypt.CompareHashAndPassword([]byte(authsvc.DummyPasswordHash), []byte("wrongpass"))
	oneCompare := time.Since(start)

	body := `{"email":"nonexistent-timing@example.com","password":"wrongpass"}`
	start = time.Now()
	resp := s.Post("/v1/auth/login", support.Session{}, body)
	elapsed := time.Since(start)

	s.AssertError(resp, 401, "unauthorized", "invalid credentials")
	s.GreaterOrEqual(elapsed, oneCompare/2, "an unknown email must cost about one bcrypt compare, took %s against %s", elapsed, oneCompare)
}

// TestRecover_AlwaysReturns200 ensures user enumeration is not possible (ForgotPassword handler).
func (s *AuthControllerTestSuite) TestRecover_Always_Returns200() {
	body := `{"email":"nobody@example.com"}`
	resp := s.Post("/v1/auth/recover", support.Session{}, body)
	resp.AssertOk()
}

// TestRecoverConfirm_InvalidToken returns 401 for a bad token (ResetPassword handler).
func (s *AuthControllerTestSuite) TestRecover_Confirm_InvalidToken() {
	body := `{"token":"invalid-token","new_password":"newpass123"}`
	resp := s.Post("/v1/auth/recover/confirm", support.Session{}, body)
	s.AssertError(resp, 401, "unauthorized", "invalid or expired token")
}

// TestLogout_NoAuth returns 401 without a bearer token.
func (s *AuthControllerTestSuite) TestLogout_No_Auth() {
	resp := s.Post("/v1/auth/logout", support.Session{}, nil)
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestRegister_PersistsUser proves POST /v1/auth/register creates the user.
// It used to answer 500 because a nil preferences pointer was written as NULL
// into the NOT NULL column.
func (s *AuthControllerTestSuite) TestRegister_Persists_User() {
	fixtures.TestDB(s.T())
	body := `{"email":"register-ok@example.com","password":"secret123","full_name":"Reg User","organization_name":"Reg Org"}`
	resp := s.Post("/v1/auth/register", support.Session{}, body)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"access_token"`, content)
	var parsed struct {
		AccessToken string `json:"access_token"`
		User        struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Equal("register-ok@example.com", parsed.User.Email)
	s.NotEmpty(parsed.AccessToken)
}

// TestRefreshToken_InvalidToken returns 401 for a bad refresh token.
func (s *AuthControllerTestSuite) TestRefresh_Token_InvalidToken() {
	body := `{"refresh_token":"bad-token-value"}`
	resp := s.Post("/v1/auth/refresh", support.Session{}, body)
	s.AssertError(resp, 401, "unauthorized", "invalid or expired refresh token")
}
