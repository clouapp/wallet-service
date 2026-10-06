package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type AccountRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.AccountRepository
}

func TestAccount_Repository_Suite(t *testing.T) {
	suite.Run(t, new(AccountRepositoryTestSuite))
}

func (s *AccountRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewAccountRepository(nil)
}

func (s *AccountRepositoryTestSuite) TestAccountRepository_Create_Success() {
	acc := &models.Account{ID: uuid.New(), Name: "Test Account", Status: "active"}
	err := s.repo.Create(context.Background(), acc)
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), acc.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("Test Account", found.Name)
}

func (s *AccountRepositoryTestSuite) TestFind_ByID_Found() {
	acc := &models.Account{ID: uuid.New(), Name: "Find Me", Status: "active"}
	s.Require().NoError(s.repo.Create(context.Background(), acc))

	found, err := s.repo.FindByID(context.Background(), acc.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal(acc.ID, found.ID)
}

func (s *AccountRepositoryTestSuite) TestAccountRepository_Exists_Succeeds() {
	missing, err := s.repo.Exists(context.Background(), uuid.New())
	s.NoError(err)
	s.False(missing)

	nilID, err := s.repo.Exists(context.Background(), uuid.Nil)
	s.NoError(err)
	s.False(nilID)

	acc := &models.Account{ID: uuid.New(), Name: "Exists", Status: "active"}
	s.Require().NoError(s.repo.Create(context.Background(), acc))
	found, err := s.repo.Exists(context.Background(), acc.ID)
	s.NoError(err)
	s.True(found)
}

func (s *AccountRepositoryTestSuite) TestFind_ByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *AccountRepositoryTestSuite) TestFind_By_IDs() {
	a1 := &models.Account{ID: uuid.New(), Name: "A1", Status: "active"}
	a2 := &models.Account{ID: uuid.New(), Name: "A2", Status: "active"}
	a3 := &models.Account{ID: uuid.New(), Name: "A3", Status: "active"}
	s.Require().NoError(s.repo.Create(context.Background(), a1))
	s.Require().NoError(s.repo.Create(context.Background(), a2))
	s.Require().NoError(s.repo.Create(context.Background(), a3))

	results, err := s.repo.FindByIDs(context.Background(), []uuid.UUID{a1.ID, a3.ID})
	s.NoError(err)
	s.Len(results, 2)
}

func (s *AccountRepositoryTestSuite) TestFind_ByIDs_Empty() {
	results, err := s.repo.FindByIDs(context.Background(), []uuid.UUID{})
	s.NoError(err)
	s.Len(results, 0)
}

// createUser inserts via raw SQL so the NOT NULL preferences column takes its
// '{}' default; account_users.user_id is a foreign key to users.
func (s *AccountRepositoryTestSuite) createUser() uuid.UUID {
	userID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, "member-"+userID.String()[:8]+"@example.com", "unused", "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *AccountRepositoryTestSuite) createMemberAccount(userID uuid.UUID, name, environment string) models.Account {
	acc := models.Account{ID: uuid.New(), Name: name, Status: "active", Environment: environment}
	s.Require().NoError(s.repo.Create(context.Background(), &acc))
	s.Require().NoError(repositories.NewAccountUserRepository(nil).Create(context.Background(), &models.AccountUser{
		ID: uuid.New(), AccountID: acc.ID, UserID: userID, Role: "owner",
	}))
	return acc
}

func accountNames(accounts []models.Account) []string {
	names := make([]string, 0, len(accounts))
	for _, acc := range accounts {
		names = append(names, acc.Name)
	}
	return names
}

func (s *AccountRepositoryTestSuite) TestPaginate_ByMember_OrdersByNameAcrossPages() {
	userID := s.createUser()
	for _, name := range []string{"Delta", "alpha", "Charlie", "Bravo", "Echo"} {
		s.createMemberAccount(userID, name, models.EnvironmentProd)
	}

	first, total, err := s.repo.PaginateByMember(context.Background(), userID, "", "", 2, 0)
	s.Require().NoError(err)
	s.Equal(int64(5), total)
	s.Equal([]string{"alpha", "Bravo"}, accountNames(first))

	last, total, err := s.repo.PaginateByMember(context.Background(), userID, "", "", 2, 4)
	s.Require().NoError(err)
	s.Equal(int64(5), total)
	s.Equal([]string{"Echo"}, accountNames(last))
}

func (s *AccountRepositoryTestSuite) TestPaginate_ByMember_OutOfRangeOffsetReturnsEmptyPageWithTotal() {
	userID := s.createUser()
	s.createMemberAccount(userID, "Only", models.EnvironmentProd)

	accounts, total, err := s.repo.PaginateByMember(context.Background(), userID, "", "", 20, 40)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.NotNil(accounts)
	s.Empty(accounts)
}

func (s *AccountRepositoryTestSuite) TestPaginate_ByMember_NoMemberships() {
	accounts, total, err := s.repo.PaginateByMember(context.Background(), uuid.New(), "", "", 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(0), total)
	s.NotNil(accounts)
	s.Empty(accounts)
}

func (s *AccountRepositoryTestSuite) TestPaginate_ByMember_ExcludesOtherUsersAndRemovedMemberships() {
	userID := s.createUser()
	kept := s.createMemberAccount(userID, "Kept", models.EnvironmentProd)
	removed := s.createMemberAccount(userID, "Removed", models.EnvironmentProd)
	s.createMemberAccount(s.createUser(), "Someone else", models.EnvironmentProd)
	s.Require().NoError(repositories.NewAccountUserRepository(nil).SoftDeleteByAccountAndUser(context.Background(), removed.ID, userID))

	accounts, total, err := s.repo.PaginateByMember(context.Background(), userID, "", "", 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Require().Len(accounts, 1)
	s.Equal(kept.ID, accounts[0].ID)
}

func (s *AccountRepositoryTestSuite) TestPaginate_ByMember_FiltersByEnvironment() {
	userID := s.createUser()
	s.createMemberAccount(userID, "Acme Corp", models.EnvironmentProd)
	s.createMemberAccount(userID, "Acme Corp (Test)", models.EnvironmentTest)

	accounts, total, err := s.repo.PaginateByMember(context.Background(), userID, "", models.EnvironmentTest, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Equal([]string{"Acme Corp (Test)"}, accountNames(accounts))
}

func (s *AccountRepositoryTestSuite) TestPaginate_ByMember_SearchesNameCaseInsensitivelyAndById() {
	userID := s.createUser()
	custody := s.createMemberAccount(userID, "Custody Desk", models.EnvironmentProd)
	s.createMemberAccount(userID, "Treasury", models.EnvironmentProd)

	byName, total, err := s.repo.PaginateByMember(context.Background(), userID, "cUsToDy", "", 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Equal([]string{"Custody Desk"}, accountNames(byName))

	byID, total, err := s.repo.PaginateByMember(context.Background(), userID, custody.ID.String()[:8], "", 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Equal(custody.ID, byID[0].ID)
}

func (s *AccountRepositoryTestSuite) TestPaginate_ByMember_TreatsLikeWildcardsLiterally() {
	userID := s.createUser()
	s.createMemberAccount(userID, "100% Reserve", models.EnvironmentProd)
	s.createMemberAccount(userID, "1000 Reserve", models.EnvironmentProd)
	s.createMemberAccount(userID, "snake_case", models.EnvironmentProd)
	s.createMemberAccount(userID, "snakeXcase", models.EnvironmentProd)

	percent, _, err := s.repo.PaginateByMember(context.Background(), userID, "100%", "", 20, 0)
	s.Require().NoError(err)
	s.Equal([]string{"100% Reserve"}, accountNames(percent))

	underscore, _, err := s.repo.PaginateByMember(context.Background(), userID, "e_c", "", 20, 0)
	s.Require().NoError(err)
	s.Equal([]string{"snake_case"}, accountNames(underscore))
}

func (s *AccountRepositoryTestSuite) TestList_Orders_ByCreatedAtDescending() {
	olderID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	sameTimeHigherID := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	newerID := uuid.MustParse("00000000-0000-4000-8000-000000000003")
	sameTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newerAt := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	s.Require().NoError(s.repo.Create(context.Background(), &models.Account{ID: olderID, Name: "Older", Status: models.StatusActive}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.Account{ID: sameTimeHigherID, Name: "Tied", Status: models.AccountStatusFrozen}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.Account{ID: newerID, Name: "Newer", Status: models.AccountStatusArchived}))
	s.stampAccountCreatedAt(olderID, sameTime)
	s.stampAccountCreatedAt(sameTimeHigherID, sameTime)
	s.stampAccountCreatedAt(newerID, newerAt)

	first, total, err := s.repo.List(context.Background(), 1, 0)
	s.Require().NoError(err)
	s.Equal(int64(3), total)
	s.Require().Len(first, 1)
	s.Equal(newerID, first[0].ID)
	s.Equal(models.AccountStatusArchived, first[0].Status)

	second, total, err := s.repo.List(context.Background(), 1, 1)
	s.Require().NoError(err)
	s.Equal(int64(3), total)
	s.Require().Len(second, 1)
	s.Equal(sameTimeHigherID, second[0].ID)

	third, total, err := s.repo.List(context.Background(), 1, 2)
	s.Require().NoError(err)
	s.Equal(olderID, third[0].ID)

	past, total, err := s.repo.List(context.Background(), 20, 3)
	s.Require().NoError(err)
	s.Equal(int64(3), total)
	s.Empty(past)

	_, _, err = s.repo.List(context.Background(), 0, 0)
	s.EqualError(err, "list accounts: limit and offset are invalid")
	_, _, err = s.repo.List(nil, 20, 0)
	s.EqualError(err, "list accounts: context is required")
}

func (s *AccountRepositoryTestSuite) stampAccountCreatedAt(id uuid.UUID, at time.Time) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(`UPDATE accounts SET created_at = ? WHERE id = ?`, at, id)
	s.Require().NoError(err)
}

func (s *AccountRepositoryTestSuite) TestAccountRepository_Set_Name() {
	acc := &models.Account{ID: uuid.New(), Name: "Old Name", Status: "active"}
	s.Require().NoError(s.repo.Create(context.Background(), acc))

	err := s.repo.SetName(context.Background(), acc.ID, "New Name")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), acc.ID)
	s.NoError(err)
	s.Equal("New Name", found.Name)
}

func (s *AccountRepositoryTestSuite) TestAccountRepository_Set_Environment() {
	acc := &models.Account{ID: uuid.New(), Name: "Acme", Status: "active", Environment: models.EnvironmentProd}
	s.Require().NoError(s.repo.Create(context.Background(), acc))

	err := s.repo.SetEnvironment(context.Background(), acc.ID, models.EnvironmentTest)
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), acc.ID)
	s.NoError(err)
	s.Equal(models.EnvironmentTest, found.Environment)
	s.Equal("Acme", found.Name)
	s.Equal("active", found.Status)
}
