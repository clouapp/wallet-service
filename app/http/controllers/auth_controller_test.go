package controllers_test

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type AuthControllerTestSuite struct {
	authSuite
}

func TestAuthControllerSuite(t *testing.T) {
	suite.Run(t, new(AuthControllerTestSuite))
}

// TestRegister_MissingBody returns 400 when no JSON body is provided.
func (s *AuthControllerTestSuite) TestRegister_MissingBody() {
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/register", nil)
	s.Require().NoError(err)
	resp.AssertStatus(400)
}

// TestRegister_MissingEmail returns 422 when email is absent (validation errors).
func (s *AuthControllerTestSuite) TestRegister_MissingEmail() {
	body := `{"password":"secret123"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/register", toReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(422)
}

// TestLogin_InvalidCredentials returns 401 for an unknown email.
func (s *AuthControllerTestSuite) TestLogin_InvalidCredentials() {
	body := `{"email":"nonexistent@example.com","password":"wrongpass"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", toReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestRecover_AlwaysReturns200 ensures user enumeration is not possible (ForgotPassword handler).
func (s *AuthControllerTestSuite) TestRecover_AlwaysReturns200() {
	body := `{"email":"nobody@example.com"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/recover", toReader(body))
	s.Require().NoError(err)
	resp.AssertOk()
}

// TestRecoverConfirm_InvalidToken returns 401 for a bad token (ResetPassword handler).
func (s *AuthControllerTestSuite) TestRecoverConfirm_InvalidToken() {
	body := `{"token":"invalid-token","new_password":"newpass123"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/recover/confirm", toReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestLogout_NoAuth returns 401 without a bearer token.
func (s *AuthControllerTestSuite) TestLogout_NoAuth() {
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/logout", nil)
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestRefreshToken_InvalidToken returns 401 for a bad refresh token.
func (s *AuthControllerTestSuite) TestRefreshToken_InvalidToken() {
	body := `{"refresh_token":"bad-token-value"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/refresh", toReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}
