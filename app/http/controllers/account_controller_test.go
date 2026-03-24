package controllers_test

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type AccountControllerTestSuite struct {
	authSuite
}

func TestAccountControllerSuite(t *testing.T) {
	suite.Run(t, new(AccountControllerTestSuite))
}

// TestCreateAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestCreateAccount_Unauthenticated() {
	body := `{"name":"My Account"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/accounts/", toReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestGetAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestGetAccount_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Get("/v1/accounts/00000000-0000-0000-0000-000000000001")
	s.Require().NoError(err)
	// SessionAuth will reject before AccountContext runs
	resp.AssertStatus(401)
}

// TestUpdateAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestUpdateAccount_Unauthenticated() {
	body := `{"name":"New Name"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Patch("/v1/accounts/00000000-0000-0000-0000-000000000001", toReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestFreezeAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestFreezeAccount_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Post("/v1/accounts/00000000-0000-0000-0000-000000000001/freeze", nil)
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestArchiveAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestArchiveAccount_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Post("/v1/accounts/00000000-0000-0000-0000-000000000001/archive", nil)
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestListAccountUsers_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestListAccountUsers_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Get("/v1/accounts/00000000-0000-0000-0000-000000000001/users")
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestListAccountTokens_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestListAccountTokens_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Get("/v1/accounts/00000000-0000-0000-0000-000000000001/tokens")
	s.Require().NoError(err)
	resp.AssertStatus(401)
}
