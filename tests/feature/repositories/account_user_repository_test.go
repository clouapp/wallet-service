package repositories_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type AccountUserRepositoryTestSuite struct {
	suite.Suite
	repo    *repositories.AccountUserRepository
	accRepo *repositories.AccountRepository
}

func TestAccount_User_RepositorySuite(t *testing.T) {
	suite.Run(t, new(AccountUserRepositoryTestSuite))
}

func (s *AccountUserRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
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

func (s *AccountUserRepositoryTestSuite) TestAccountUserRepository_Create_Success() {
	accID := s.createAccount()
	au := &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: s.createUser(), Role: "owner"}
	err := s.repo.Create(context.Background(), au)
	s.NoError(err)
}

func (s *AccountUserRepositoryTestSuite) TestFind_By_ID() {
	accID := s.createAccount()
	userID := s.createUser()
	au := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: accID,
		UserID:    userID,
		Role:      "auditor",
		Status:    models.MembershipStatusSuspended,
	}
	s.Require().NoError(s.repo.Create(context.Background(), au))

	found, err := s.repo.FindByID(context.Background(), au.ID)
	s.Require().NoError(err)
	s.Require().NotNil(found)
	s.Equal(au.ID, found.ID)
	s.Equal("auditor", found.Role)
	s.Equal(models.MembershipStatusSuspended, found.Status)

	missing, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(missing)
}

func (s *AccountUserRepositoryTestSuite) TestFind_By_AccountID() {
	accID := s.createAccount()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: s.createUser(), Role: "owner"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: s.createUser(), Role: "admin"}))

	members, err := s.repo.FindByAccountID(context.Background(), accID)
	s.Require().NoError(err)
	s.Len(members, 2)
}

func (s *AccountUserRepositoryTestSuite) TestFind_ByAccountAndUser_Found() {
	accID := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "admin"}))

	au, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.Require().NoError(err)
	s.Require().NotNil(au)
	s.Equal("admin", au.Role)
}

func (s *AccountUserRepositoryTestSuite) TestFind_ByAccountAndUser_NotFound() {
	au, err := s.repo.FindByAccountAndUser(context.Background(), uuid.New(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(au)
}

func (s *AccountUserRepositoryTestSuite) TestFind_By_AccountAndUserIncludeDeleted() {
	accID := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "admin"}))

	err := s.repo.SoftDeleteByAccountAndUser(context.Background(), accID, userID)
	s.Require().NoError(err)

	active, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(active)

	withDeleted, err := s.repo.FindByAccountAndUserIncludeDeleted(context.Background(), accID, userID)
	s.Require().NoError(err)
	s.Require().NotNil(withDeleted)
	s.NotNil(withDeleted.DeletedAt)
}

func (s *AccountUserRepositoryTestSuite) TestRoles_ForUserAccounts_ReturnsStoredRoles() {
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

func (s *AccountUserRepositoryTestSuite) TestRoles_ForUserAccounts_SkipsRemovedMembership() {
	accountID := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "auditor"}))
	s.Require().NoError(s.repo.SoftDeleteByAccountAndUser(context.Background(), accountID, userID))

	roles, err := s.repo.RolesForUserAccounts(context.Background(), userID, []uuid.UUID{accountID})
	s.Require().NoError(err)
	s.Empty(roles)
}

func (s *AccountUserRepositoryTestSuite) TestFind_By_UserID() {
	acc1 := s.createAccount()
	acc2 := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: acc1, UserID: userID, Role: "owner"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: acc2, UserID: userID, Role: "admin"}))

	memberships, err := s.repo.FindByUserID(context.Background(), userID)
	s.Require().NoError(err)
	s.Len(memberships, 2)
}

func (s *AccountUserRepositoryTestSuite) TestAccess_Lookups_IgnoreMembershipsThatAreNotActive() {
	activeAccount := s.createAccount()
	suspendedAccount := s.createAccount()
	userID := insertActiveUserRow(s.T())
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: activeAccount, UserID: userID, Role: "owner", Status: models.StatusActive}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: suspendedAccount, UserID: userID, Role: "owner", Status: "suspended"}))

	active, err := s.repo.FindByAccountAndUser(context.Background(), activeAccount, userID)
	s.Require().NoError(err)
	s.NotNil(active)
	suspended, err := s.repo.FindByAccountAndUser(context.Background(), suspendedAccount, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(suspended)

	memberships, err := s.repo.FindByUserID(context.Background(), userID)
	s.Require().NoError(err)
	s.Require().Len(memberships, 1)
	s.Equal(activeAccount, memberships[0].AccountID)

	page, total, err := s.repo.PaginateByUserID(context.Background(), userID, 10, 0)
	s.Require().NoError(err)
	s.EqualValues(1, total)
	s.Len(page, 1)

	managed, err := s.repo.FindByAccountAndUserIncludeDeleted(context.Background(), suspendedAccount, userID)
	s.Require().NoError(err)
	s.Require().NotNil(managed)
	s.Equal("suspended", managed.Status)
}

func (s *AccountUserRepositoryTestSuite) TestAccountUserRepository_Set_Role() {
	accID := s.createAccount()
	userID := s.createUser()
	au := &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "auditor"}
	s.Require().NoError(s.repo.Create(context.Background(), au))

	err := s.repo.SetRole(context.Background(), au.ID, "admin")
	s.Require().NoError(err)

	found, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.Require().NoError(err)
	s.Equal("admin", found.Role)
}

func (s *AccountUserRepositoryTestSuite) TestSet_Status_AndCountActiveOwners() {
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

func (s *AccountUserRepositoryTestSuite) TestSoft_Delete_ByAccountAndUser() {
	accID := s.createAccount()
	userID := s.createUser()
	s.Require().NoError(s.repo.Create(context.Background(), &models.AccountUser{ID: uuid.New(), AccountID: accID, UserID: userID, Role: "user"}))

	err := s.repo.SoftDeleteByAccountAndUser(context.Background(), accID, userID)
	s.Require().NoError(err)

	found, err := s.repo.FindByAccountAndUser(context.Background(), accID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

// insertMembershipAt stores a membership whose created_at is at, in the order the
// test inserts them, so neither the heap nor an index orders the rows by time.
func (s *AccountUserRepositoryTestSuite) insertMembershipAt(accountID, userID uuid.UUID, at time.Time) uuid.UUID {
	membership := &models.AccountUser{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "admin", Status: models.StatusActive}
	s.Require().NoError(s.repo.Create(context.Background(), membership))
	_, err := facades.Orm().Query().Exec(`UPDATE account_users SET created_at = ? WHERE id = ?`, at, membership.ID)
	s.Require().NoError(err)
	return membership.ID
}

// The pages are in the order the memberships were created, so offset and limit
// walk every member exactly once.
func (s *AccountUserRepositoryTestSuite) TestPaginate_Pages_FollowTheMembershipsCreationOrder() {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := []uuid.UUID{s.createUser(), s.createUser(), s.createUser()}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() > ids[j].String() })
	oldest, middle, newest := ids[0], ids[1], ids[2]

	accountID := s.createAccount()
	created := map[uuid.UUID]uuid.UUID{}
	created[middle] = s.insertMembershipAt(accountID, middle, base.Add(time.Hour))
	created[newest] = s.insertMembershipAt(accountID, newest, base.Add(2*time.Hour))
	created[oldest] = s.insertMembershipAt(accountID, oldest, base)

	first, total, err := s.repo.PaginateByAccountID(context.Background(), accountID, 2, 0)
	s.Require().NoError(err)
	second, _, err := s.repo.PaginateByAccountID(context.Background(), accountID, 2, 2)
	s.Require().NoError(err)
	s.EqualValues(3, total)
	s.Equal([]uuid.UUID{created[oldest], created[middle], created[newest]}, membershipIDs(append(first, second...)))

	userID := s.createUser()
	accounts := []uuid.UUID{s.createAccount(), s.createAccount(), s.createAccount()}
	byUser := []uuid.UUID{
		s.insertMembershipAt(accounts[0], userID, base.Add(2*time.Hour)),
		s.insertMembershipAt(accounts[1], userID, base),
		s.insertMembershipAt(accounts[2], userID, base.Add(time.Hour)),
	}
	page, _, err := s.repo.PaginateByUserID(context.Background(), userID, 2, 0)
	s.Require().NoError(err)
	rest, _, err := s.repo.PaginateByUserID(context.Background(), userID, 2, 2)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{byUser[1], byUser[2], byUser[0]}, membershipIDs(append(page, rest...)))
}

func membershipIDs(memberships []models.AccountUser) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(memberships))
	for _, membership := range memberships {
		ids = append(ids, membership.ID)
	}
	return ids
}
