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

type AccountRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.AccountRepository
}

func TestAccountRepositorySuite(t *testing.T) {
	suite.Run(t, new(AccountRepositoryTestSuite))
}

func (s *AccountRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewAccountRepository(nil)
}

func (s *AccountRepositoryTestSuite) TestCreate_Success() {
	acc := &models.Account{ID: uuid.New(), Name: "Test Account", Status: "active"}
	err := s.repo.Create(context.Background(), acc)
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), acc.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("Test Account", found.Name)
}

func (s *AccountRepositoryTestSuite) TestFindByID_Found() {
	acc := &models.Account{ID: uuid.New(), Name: "Find Me", Status: "active"}
	s.Require().NoError(s.repo.Create(context.Background(), acc))

	found, err := s.repo.FindByID(context.Background(), acc.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal(acc.ID, found.ID)
}

func (s *AccountRepositoryTestSuite) TestFindByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *AccountRepositoryTestSuite) TestFindByIDs() {
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

func (s *AccountRepositoryTestSuite) TestFindByIDs_Empty() {
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

func (s *AccountRepositoryTestSuite) TestPaginateByMember_OrdersByNameAcrossPages() {
	userID := s.createUser()
	for _, name := range []string{"Delta", "alpha", "Charlie", "Bravo", "Echo"} {
		s.createMemberAccount(userID, name, models.EnvironmentProd)
	}

	first, total, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{}, 2, 0)
	s.Require().NoError(err)
	s.Equal(int64(5), total)
	s.Equal([]string{"alpha", "Bravo"}, accountNames(first))

	last, total, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{}, 2, 4)
	s.Require().NoError(err)
	s.Equal(int64(5), total)
	s.Equal([]string{"Echo"}, accountNames(last))
}

func (s *AccountRepositoryTestSuite) TestPaginateByMember_OutOfRangeOffsetReturnsEmptyPageWithTotal() {
	userID := s.createUser()
	s.createMemberAccount(userID, "Only", models.EnvironmentProd)

	accounts, total, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{}, 20, 40)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.NotNil(accounts)
	s.Empty(accounts)
}

func (s *AccountRepositoryTestSuite) TestPaginateByMember_NoMemberships() {
	accounts, total, err := s.repo.PaginateByMember(context.Background(), uuid.New(), repositories.AccountListFilter{}, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(0), total)
	s.NotNil(accounts)
	s.Empty(accounts)
}

func (s *AccountRepositoryTestSuite) TestPaginateByMember_ExcludesOtherUsersAndRemovedMemberships() {
	userID := s.createUser()
	kept := s.createMemberAccount(userID, "Kept", models.EnvironmentProd)
	removed := s.createMemberAccount(userID, "Removed", models.EnvironmentProd)
	s.createMemberAccount(s.createUser(), "Someone else", models.EnvironmentProd)
	s.Require().NoError(repositories.NewAccountUserRepository(nil).SoftDeleteByAccountAndUser(context.Background(), removed.ID, userID))

	accounts, total, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{}, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Require().Len(accounts, 1)
	s.Equal(kept.ID, accounts[0].ID)
}

func (s *AccountRepositoryTestSuite) TestPaginateByMember_FiltersByEnvironment() {
	userID := s.createUser()
	s.createMemberAccount(userID, "Acme Corp", models.EnvironmentProd)
	s.createMemberAccount(userID, "Acme Corp (Test)", models.EnvironmentTest)

	accounts, total, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{Environment: models.EnvironmentTest}, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Equal([]string{"Acme Corp (Test)"}, accountNames(accounts))
}

func (s *AccountRepositoryTestSuite) TestPaginateByMember_SearchesNameCaseInsensitivelyAndById() {
	userID := s.createUser()
	custody := s.createMemberAccount(userID, "Custody Desk", models.EnvironmentProd)
	s.createMemberAccount(userID, "Treasury", models.EnvironmentProd)

	byName, total, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{Search: "cUsToDy"}, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Equal([]string{"Custody Desk"}, accountNames(byName))

	byID, total, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{Search: custody.ID.String()[:8]}, 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Equal(custody.ID, byID[0].ID)
}

func (s *AccountRepositoryTestSuite) TestPaginateByMember_TreatsLikeWildcardsLiterally() {
	userID := s.createUser()
	s.createMemberAccount(userID, "100% Reserve", models.EnvironmentProd)
	s.createMemberAccount(userID, "1000 Reserve", models.EnvironmentProd)
	s.createMemberAccount(userID, "snake_case", models.EnvironmentProd)
	s.createMemberAccount(userID, "snakeXcase", models.EnvironmentProd)

	percent, _, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{Search: "100%"}, 20, 0)
	s.Require().NoError(err)
	s.Equal([]string{"100% Reserve"}, accountNames(percent))

	underscore, _, err := s.repo.PaginateByMember(context.Background(), userID, repositories.AccountListFilter{Search: "e_c"}, 20, 0)
	s.Require().NoError(err)
	s.Equal([]string{"snake_case"}, accountNames(underscore))
}

func (s *AccountRepositoryTestSuite) TestSetName() {
	acc := &models.Account{ID: uuid.New(), Name: "Old Name", Status: "active"}
	s.Require().NoError(s.repo.Create(context.Background(), acc))

	err := s.repo.SetName(context.Background(), acc.ID, "New Name")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), acc.ID)
	s.NoError(err)
	s.Equal("New Name", found.Name)
}
