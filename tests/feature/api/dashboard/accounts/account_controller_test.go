package accounts

import (
	"strings"
	"testing"

	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"
)

// AccountControllerTestSuite verifies that every /v1/accounts/* dashboard
// endpoint rejects unauthenticated requests with 401. These are SessionAuth-
// guarded routes, and we only assert the rejection surface — exercising
// authenticated paths requires a live session JWT mint helper which has not
// been extracted yet (tracked in docs/superpowers/specs/2026-04-18-base-
// address-sweep-follow-ups.md). Any test that would need SetupSessionAuth
// remains deferred until the helper lands.
type AccountControllerTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccount_Controller_Suite(t *testing.T) {
	suite.Run(t, new(AccountControllerTestSuite))
}

// TestCreateAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestCreate_Account_Unauthenticated() {
	body := `{"name":"My Account"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/accounts", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestGetAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestGet_Account_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Get("/v1/accounts/00000000-0000-0000-0000-000000000001")
	s.Require().NoError(err)
	// SessionAuth will reject before AccountContext runs
	resp.AssertStatus(401)
}

// TestUpdateAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestUpdate_Account_Unauthenticated() {
	body := `{"name":"New Name"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Patch("/v1/accounts/00000000-0000-0000-0000-000000000001", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestFreezeAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestFreeze_Account_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Post("/v1/accounts/00000000-0000-0000-0000-000000000001/freeze", nil)
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestArchiveAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestArchive_Account_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Post("/v1/accounts/00000000-0000-0000-0000-000000000001/archive", nil)
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestUpdateAccountUser_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestUpdate_AccountUser_Unauthenticated() {
	body := `{"role":"admin"}`
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Patch("/v1/accounts/00000000-0000-0000-0000-000000000001/users/00000000-0000-0000-0000-000000000002", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestListAccountInvites_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestList_AccountInvites_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Get("/v1/accounts/00000000-0000-0000-0000-000000000001/invites")
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestListAccountUsers_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestList_AccountUsers_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Get("/v1/accounts/00000000-0000-0000-0000-000000000001/users")
	s.Require().NoError(err)
	resp.AssertStatus(401)
}

// TestListAccountTokens_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestList_AccountTokens_Unauthenticated() {
	resp, err := s.Http(s.T()).
		Get("/v1/accounts/00000000-0000-0000-0000-000000000001/tokens")
	s.Require().NoError(err)
	resp.AssertStatus(401)
}
