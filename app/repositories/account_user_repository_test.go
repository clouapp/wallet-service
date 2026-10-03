package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type AccountUserRepositoryTestSuite struct {
	suite.Suite
	repo    *repositories.AccountUserRepository
	accRepo *repositories.AccountRepository
}

func TestAccountUserRepositorySuite(t *testing.T) {
	suite.Run(t, new(AccountUserRepositoryTestSuite))
}

func (s *AccountUserRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewAccountUserRepository(nil)
	s.accRepo = repositories.NewAccountRepository(nil)
}

func (s *AccountUserRepositoryTestSuite) createAccount() uuid.UUID {
	acc := &models.Account{ID: uuid.New(), Name: "Acc " + uuid.NewString()[:8], Status: "active"}
	s.Require().NoError(s.accRepo.Create(context.Background(), acc))
	return acc.ID
}

func (s *AccountUserRepositoryTestSuite) createUser() uuid.UUID {
	userID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, "member-"+userID.String()[:8]+"@example.com", "unused", "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *AccountUserRepositoryTestSuite) TestCreate_Success() {
	accID := s.createAccount()
	au := &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: s.createUser(), Role: "owner"}
	err := s.repo.Create(context.Background(), au)
	s.NoError(err)
}

func (s *AccountUserRepositoryTestSuite) TestFindByAccountID() {
	accID := s.createAccount()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: s.createUser(), Role: "owner"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: s.createUser(), Role: "admin"}))

	members, err := s.repo.FindByAccountID(context.Background(), accID)
	s.NoError(err)
	s.Len(members, 2)
}

func (s *AccountUserRepositoryTestSuite) TestFindByAccountAndUser_Found() {
	accID := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "admin"}))

	au, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.NoError(err)
	s.NotNil(au)
	s.Equal("admin", au.Role)
}

func (s *AccountUserRepositoryTestSuite) TestFindByAccountAndUser_NotFound() {
	au, err := s.repo.FindByAccountAndUser(context.Background(), uuid.New(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(au)
}

func (s *AccountUserRepositoryTestSuite) TestFindByAccountAndUserIncludeDeleted() {
	accID := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "admin"}))

	err := s.repo.SoftDeleteByAccountAndUser(context.Background(), accID, userID)
	s.Require().NoError(err)

	active, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(active)

	withDeleted, err := s.repo.FindByAccountAndUserIncludeDeleted(context.Background(), accID, userID)
	s.NoError(err)
	s.NotNil(withDeleted)
	s.NotNil(withDeleted.DeletedAt)
}

func (s *AccountUserRepositoryTestSuite) TestRolesForUserAccounts_ReturnsStoredRoles() {
	ownerAccount := s.createAccount()
	auditorAccount := s.createAccount()
	otherAccount := s.createAccount()
	userID := s.createUser()
	otherUserID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: ownerAccount, UserID: userID, Role: "owner"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: auditorAccount, UserID: userID, Role: "auditor"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: otherAccount, UserID: otherUserID, Role: "admin"}))

	roles, err := s.repo.RolesForUserAccounts(context.Background(), userID, []uuid.UUID{ownerAccount, auditorAccount, otherAccount})
	s.Require().NoError(err)
	s.Equal(map[uuid.UUID]string{ownerAccount: "owner", auditorAccount: "auditor"}, roles)

	empty, err := s.repo.RolesForUserAccounts(context.Background(), userID, nil)
	s.Require().NoError(err)
	s.Empty(empty)

	_, err = s.repo.RolesForUserAccounts(context.Background(), uuid.Nil, []uuid.UUID{ownerAccount})
	s.Error(err)
}

func (s *AccountUserRepositoryTestSuite) TestRolesForUserAccounts_SkipsRemovedMembership() {
	accountID := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "auditor"}))
	s.Require().NoError(s.repo.SoftDeleteByAccountAndUser(context.Background(), accountID, userID))

	roles, err := s.repo.RolesForUserAccounts(context.Background(), userID, []uuid.UUID{accountID})
	s.Require().NoError(err)
	s.Empty(roles)
}

func (s *AccountUserRepositoryTestSuite) TestFindByUserID() {
	acc1 := s.createAccount()
	acc2 := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: acc1, UserID: userID, Role: "owner"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: acc2, UserID: userID, Role: "admin"}))

	memberships, err := s.repo.FindByUserID(context.Background(), userID)
	s.NoError(err)
	s.Len(memberships, 2)
}

func (s *AccountUserRepositoryTestSuite) TestAccessLookupsIgnoreMembershipsThatAreNotActive() {
	activeAccount := s.createAccount()
	suspendedAccount := s.createAccount()
	userID := insertActiveUserRow(s.T())
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: activeAccount, UserID: userID, Role: "owner", Status: models.StatusActive}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: suspendedAccount, UserID: userID, Role: "owner", Status: "suspended"}))

	active, err := s.repo.FindByAccountAndUser(context.Background(), activeAccount, userID)
	s.NoError(err)
	s.NotNil(active)
	suspended, err := s.repo.FindByAccountAndUser(context.Background(), suspendedAccount, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(suspended)

	memberships, err := s.repo.FindByUserID(context.Background(), userID)
	s.NoError(err)
	s.Require().Len(memberships, 1)
	s.Equal(activeAccount, memberships[0].AccountID)

	page, total, err := s.repo.PaginateByUserID(context.Background(), userID, 10, 0)
	s.NoError(err)
	s.EqualValues(1, total)
	s.Len(page, 1)

	managed, err := s.repo.FindByAccountAndUserIncludeDeleted(context.Background(), suspendedAccount, userID)
	s.NoError(err)
	s.Require().NotNil(managed)
	s.Equal("suspended", managed.Status)
}

func (s *AccountUserRepositoryTestSuite) TestSetRole() {
	accID := s.createAccount()
	userID := s.createUser()
	au := &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "auditor"}
	s.Require().NoError(s.repo.Create(context.Background(), au))

	err := s.repo.SetRole(context.Background(), au.ID, "admin")
	s.NoError(err)

	found, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.NoError(err)
	s.Equal("admin", found.Role)
}

func (s *AccountUserRepositoryTestSuite) TestSetStatusAndCountActiveOwners() {
	accID := s.createAccount()
	ownerID := s.createUser()
	suspendedID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{
		ID: uuid.New(), AccountID: accID, UserID: ownerID, Role: "owner", Status: models.MembershipStatusActive,
	}))
	suspended := &models.AccountUser{
		ID: uuid.New(), AccountID: accID, UserID: suspendedID, Role: "owner", Status: models.MembershipStatusActive,
	}
	s.Require().NoError(s.repo.Create(context.Background(), suspended))

	owners, err := s.repo.CountActiveByRole(context.Background(), accID, "owner")
	s.Require().NoError(err)
	s.Equal(int64(2), owners)

	s.Require().NoError(s.repo.SetStatus(context.Background(), suspended.ID, models.MembershipStatusSuspended))
	owners, err = s.repo.CountActiveByRole(context.Background(), accID, "owner")
	s.Require().NoError(err)
	s.Equal(int64(1), owners)

	access, err := s.repo.FindByAccountAndUser(context.Background(), accID, suspendedID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(access)

	found, err := s.repo.FindByAccountAndUserIncludeDeleted(context.Background(), accID, suspendedID)
	s.Require().NoError(err)
	s.Equal(models.MembershipStatusSuspended, found.Status)
}

func (s *AccountUserRepositoryTestSuite) TestSoftDeleteByAccountAndUser() {
	accID := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "user"}))

	err := s.repo.SoftDeleteByAccountAndUser(context.Background(), accID, userID)
	s.NoError(err)

	found, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}
