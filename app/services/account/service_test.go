package account_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	accountsvc "github.com/macrowallets/waas/app/services/account"
)

type AccountServiceTestSuite struct {
	suite.Suite
	state *memState
}

func TestService_Account_Service(t *testing.T) {
	suite.Run(t, new(AccountServiceTestSuite))
}

func (s *AccountServiceTestSuite) SetupTest() {
	s.state = newMemState()
}

func (s *AccountServiceTestSuite) service() *accountsvc.Service {
	return accountsvc.NewService(accountsvc.Deps{
		Accounts:    memAccounts{state: s.state},
		Memberships: memMemberships{state: s.state},
	})
}

// TestCreate_Success verifies that Create returns an account with "active" status
// and creates an owner membership.
func (s *AccountServiceTestSuite) TestAccountService_Create_Success() {
	svc := s.service()
	ownerID := uuid.New()
	ctx := context.Background()

	acc, err := svc.Create(ctx, "Test Account", ownerID)
	s.Require().NoError(err)
	s.Require().NotNil(acc)
	s.Equal("Test Account", acc.Name)
	s.Equal("active", acc.Status)

	role, err := svc.GetUserRole(ctx, acc.ID, ownerID)
	s.Require().NoError(err)
	s.Equal("owner", role)
}

// TestAddUser_Success verifies that AddUser adds a new member to an account.
func (s *AccountServiceTestSuite) TestAdd_User_Success() {
	svc := s.service()
	ctx := context.Background()
	ownerID := uuid.New()

	acc, err := svc.Create(ctx, "Membership Test Account", ownerID)
	s.Require().NoError(err)

	newUserID := uuid.New()
	err = svc.AddUser(ctx, acc.ID, newUserID, "admin", ownerID)
	s.Require().NoError(err)

	role, err := svc.GetUserRole(ctx, acc.ID, newUserID)
	s.Require().NoError(err)
	s.Equal("admin", role)
}

// TestAddUser_ReAdd_ClearsDeletedAt verifies that a soft-deleted member can be re-added.
func (s *AccountServiceTestSuite) TestAddUser_ReAdd_ClearsDeletedAt() {
	svc := s.service()
	ctx := context.Background()
	ownerID := uuid.New()

	acc, err := svc.Create(ctx, "ReAdd Test Account", ownerID)
	s.Require().NoError(err)

	userID := uuid.New()
	err = svc.AddUser(ctx, acc.ID, userID, "auditor", ownerID)
	s.Require().NoError(err)

	err = svc.RemoveUser(ctx, acc.ID, userID)
	s.Require().NoError(err)

	role, _ := svc.GetUserRole(ctx, acc.ID, userID)
	s.Equal("", role, "role should be empty after removal")

	err = svc.AddUser(ctx, acc.ID, userID, "user", ownerID)
	s.Require().NoError(err)

	role, err = svc.GetUserRole(ctx, acc.ID, userID)
	s.Require().NoError(err)
	s.Equal("user", role)
}

// Adding a member who is on the account already is a refusal: no second row,
// and the role they hold stays.
func (s *AccountServiceTestSuite) TestAdd_User_TwiceIsAlreadyAMember() {
	svc := s.service()
	ctx := context.Background()
	ownerID := uuid.New()

	acc, err := svc.Create(ctx, "Twice Test Account", ownerID)
	s.Require().NoError(err)
	userID := uuid.New()
	s.Require().NoError(svc.AddUser(ctx, acc.ID, userID, "admin", ownerID))

	err = svc.AddUser(ctx, acc.ID, userID, "user", ownerID)

	s.ErrorIs(err, accountsvc.ErrAlreadyMember)
	role, err := svc.GetUserRole(ctx, acc.ID, userID)
	s.Require().NoError(err)
	s.Equal("admin", role)
}

// TestIsolation_UserCannotAccessOtherAccount verifies that GetUserRole returns empty
// string when a user has no membership in the queried account.
func (s *AccountServiceTestSuite) TestIsolation_User_CannotAccessOtherAccount() {
	svc := s.service()
	ctx := context.Background()
	ownerA := uuid.New()
	ownerB := uuid.New()

	accA, err := svc.Create(ctx, "Account A", ownerA)
	s.Require().NoError(err)

	_, err = svc.Create(ctx, "Account B", ownerB)
	s.Require().NoError(err)

	role, err := svc.GetUserRole(ctx, accA.ID, ownerB)
	s.Require().NoError(err)
	s.Equal("", role)
}
