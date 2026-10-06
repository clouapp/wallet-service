package auth

import (
	"encoding/json"
	"strings"
	"testing"

	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

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
	suite.Suite
	goravelTesting.TestCase
}

func TestAuth_Controller_Suite(t *testing.T) {
	suite.Run(t, new(AuthControllerTestSuite))
}

// TestRegister_MissingBody returns 400 when no JSON body is provided.
func (s *AuthControllerTestSuite) TestRegister_Missing_Body() {
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/register", nil)
	s.Require().NoError(err)
	resp.AssertStatus(400)
}

// TestRegister_MissingEmail returns 422 when email is absent (validation errors).
func (s *AuthControllerTestSuite) TestRegister_Missing_Email() {
	body := `{"password":"secret123"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/register", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(422)
}

// TestLogin_InvalidCredentials returns 401 for an unknown email.
func (s *AuthControllerTestSuite) TestLogin_Invalid_Credentials() {
	body := `{"email":"nonexistent@example.com","password":"wrongpass"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestRecover_AlwaysReturns200 ensures user enumeration is not possible (ForgotPassword handler).
func (s *AuthControllerTestSuite) TestRecover_Always_Returns200() {
	body := `{"email":"nobody@example.com"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/recover", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertOk()
}

// TestRecoverConfirm_InvalidToken returns 401 for a bad token (ResetPassword handler).
func (s *AuthControllerTestSuite) TestRecover_Confirm_InvalidToken() {
	body := `{"token":"invalid-token","new_password":"newpass123"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/recover/confirm", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestLogout_NoAuth returns 401 without a bearer token.
func (s *AuthControllerTestSuite) TestLogout_No_Auth() {
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/logout", nil)
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestRegister_PersistsUser proves POST /v1/auth/register creates the user.
// It used to answer 500 because a nil preferences pointer was written as NULL
// into the NOT NULL column.
func (s *AuthControllerTestSuite) TestRegister_Persists_User() {
	fixtures.TestDB(s.T())
	body := `{"email":"register-ok@example.com","password":"secret123","full_name":"Reg User","organization_name":"Reg Org"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/register", strings.NewReader(body))
	s.Require().NoError(err)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Require().Contains(content, `"access_token"`, content)
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
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/refresh", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}
