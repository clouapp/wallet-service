package accounts

import (
	"testing"

	"github.com/macrowallets/waas/tests/feature/support"
)

// AccountControllerTestSuite verifies that every /v1/accounts/* dashboard
// endpoint rejects unauthenticated requests with 401. These are SessionAuth-
// guarded routes, and we only assert the rejection surface — exercising
// authenticated paths requires a live session JWT mint helper which has not
// been extracted yet (tracked in docs/superpowers/specs/2026-04-18-base-
// address-sweep-follow-ups.md). Any test that would need SetupSessionAuth
// remains deferred until the helper lands.
type AccountControllerTestSuite struct {
	support.HTTPSuite
}

func TestAccount_Controller_Suite(t *testing.T) {
	support.RunSuite(t, new(AccountControllerTestSuite))
}

// TestCreateAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestCreate_Account_Unauthenticated() {
	body := `{"name":"My Account"}`
	resp := s.Post("/v1/accounts", support.Session{}, body)
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestGetAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestGet_Account_Unauthenticated() {
	resp := s.Get("/v1/accounts/00000000-0000-0000-0000-000000000001", support.Session{})
	// SessionAuth will reject before AccountContext runs
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestUpdateAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestUpdate_Account_Unauthenticated() {
	body := `{"name":"New Name"}`
	resp := s.Patch("/v1/accounts/00000000-0000-0000-0000-000000000001", support.Session{}, body)
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestFreezeAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestFreeze_Account_Unauthenticated() {
	resp := s.Post("/v1/accounts/00000000-0000-0000-0000-000000000001/freeze", support.Session{}, nil)
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestArchiveAccount_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestArchive_Account_Unauthenticated() {
	resp := s.Post("/v1/accounts/00000000-0000-0000-0000-000000000001/archive", support.Session{}, nil)
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestUpdateAccountUser_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestUpdate_AccountUser_Unauthenticated() {
	body := `{"role":"admin"}`
	resp := s.Patch("/v1/accounts/00000000-0000-0000-0000-000000000001/users/00000000-0000-0000-0000-000000000002", support.Session{}, body)
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestListAccountInvites_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestList_AccountInvites_Unauthenticated() {
	resp := s.Get("/v1/accounts/00000000-0000-0000-0000-000000000001/invites", support.Session{})
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestListAccountUsers_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestList_AccountUsers_Unauthenticated() {
	resp := s.Get("/v1/accounts/00000000-0000-0000-0000-000000000001/users", support.Session{})
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestListAccountTokens_Unauthenticated returns 401 without a bearer token.
func (s *AccountControllerTestSuite) TestList_AccountTokens_Unauthenticated() {
	resp := s.Get("/v1/accounts/00000000-0000-0000-0000-000000000001/tokens", support.Session{})
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}
