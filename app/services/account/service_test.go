package account_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/repositories"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type AccountServiceTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestAccountService(t *testing.T) {
	suite.Run(t, new(AccountServiceTestSuite))
}

func (s *AccountServiceTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *AccountServiceTestSuite) createUser() uuid.UUID {
	userID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, "member-"+userID.String()[:8]+"@example.com", "unused", "active",
	)
	s.Require().NoError(err)
	return userID
}

// TestCreate_Success verifies that Create returns an account with "active" status
// and creates an owner membership. Requires a live database connection.
func (s *AccountServiceTestSuite) TestCreate_Success() {
	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    repositories.NewAccountRepository(nil),
		Memberships: repositories.NewAccountUserRepository(nil),
	})
	ownerID := s.createUser()
	ctx := context.Background()

	acc, err := svc.Create(ctx, "Test Account", ownerID)
	s.NoError(err)
	s.NotNil(acc)
	s.Equal("Test Account", acc.Name)
	s.Equal("active", acc.Status)

	role, err := svc.GetUserRole(ctx, acc.ID, ownerID)
	s.NoError(err)
	s.Equal("owner", role)
}

// TestAddUser_Success verifies that AddUser adds a new member to an account.
func (s *AccountServiceTestSuite) TestAddUser_Success() {
	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    repositories.NewAccountRepository(nil),
		Memberships: repositories.NewAccountUserRepository(nil),
	})
	ctx := context.Background()
	ownerID := s.createUser()

	acc, err := svc.Create(ctx, "Membership Test Account", ownerID)
	s.Require().NoError(err)

	newUserID := s.createUser()
	err = svc.AddUser(ctx, acc.ID, newUserID, "admin", ownerID)
	s.NoError(err)

	role, err := svc.GetUserRole(ctx, acc.ID, newUserID)
	s.NoError(err)
	s.Equal("admin", role)
}

// TestAddUser_ReAdd_ClearsDeletedAt verifies that a soft-deleted member can be re-added.
func (s *AccountServiceTestSuite) TestAddUser_ReAdd_ClearsDeletedAt() {
	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    repositories.NewAccountRepository(nil),
		Memberships: repositories.NewAccountUserRepository(nil),
	})
	ctx := context.Background()
	ownerID := s.createUser()

	acc, err := svc.Create(ctx, "ReAdd Test Account", ownerID)
	s.Require().NoError(err)

	userID := s.createUser()
	err = svc.AddUser(ctx, acc.ID, userID, "auditor", ownerID)
	s.Require().NoError(err)

	err = svc.RemoveUser(ctx, acc.ID, userID)
	s.Require().NoError(err)

	role, _ := svc.GetUserRole(ctx, acc.ID, userID)
	s.Equal("", role, "role should be empty after removal")

	err = svc.AddUser(ctx, acc.ID, userID, "user", ownerID)
	s.NoError(err)

	role, err = svc.GetUserRole(ctx, acc.ID, userID)
	s.NoError(err)
	s.Equal("user", role)
}

// TestIsolation_UserCannotAccessOtherAccount verifies that GetUserRole returns empty
// string when a user has no membership in the queried account.
func (s *AccountServiceTestSuite) TestIsolation_UserCannotAccessOtherAccount() {
	svc := accountsvc.NewService(accountsvc.Deps{
		Accounts:    repositories.NewAccountRepository(nil),
		Memberships: repositories.NewAccountUserRepository(nil),
	})
	ctx := context.Background()
	ownerA := s.createUser()
	ownerB := s.createUser()

	accA, err := svc.Create(ctx, "Account A", ownerA)
	s.Require().NoError(err)

	_, err = svc.Create(ctx, "Account B", ownerB)
	s.Require().NoError(err)

	// ownerB should have no role in accA
	role, err := svc.GetUserRole(ctx, accA.ID, ownerB)
	s.NoError(err)
	s.Equal("", role)
}
