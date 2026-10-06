package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// IdentityRowsSuite locks the rows the account and user queries return after
// the repository layout change.
type IdentityRowsSuite struct {
	suite.Suite
	users       *repositories.UserRepository
	accounts    *repositories.AccountRepository
	memberships *repositories.AccountUserRepository
}

func TestIdentity_Rows_Suite(t *testing.T) {
	suite.Run(t, new(IdentityRowsSuite))
}

func (s *IdentityRowsSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.users = repositories.NewUserRepository(nil)
	s.accounts = repositories.NewAccountRepository(nil)
	s.memberships = repositories.NewAccountUserRepository(nil)
}

func (s *IdentityRowsSuite) TestUser_Account_QueriesReturnTheStoredRow() {
	ctx := context.Background()
	user := &models.User{
		ID:           uuid.New(),
		Email:        "rows-" + uuid.NewString() + "@example.com",
		PasswordHash: "hash",
		FullName:     "Row User",
		Status:       "active",
	}
	s.Require().NoError(s.users.Create(ctx, user))

	account := &models.Account{ID: uuid.New(), Name: "Row Account", Status: "active", Environment: models.EnvironmentProd}
	s.Require().NoError(s.accounts.Create(ctx, account))
	membership := &models.AccountUser{
		ID:        uuid.New(),
		AccountID: account.ID,
		UserID:    user.ID,
		Role:      "owner",
	}
	s.Require().NoError(s.memberships.Create(ctx, membership))

	byEmail, err := s.users.FindByEmail(ctx, user.Email)
	s.Require().NoError(err)
	byID, err := s.users.FindByID(ctx, user.ID)
	s.Require().NoError(err)
	s.Equal(byEmail.ID, byID.ID)
	s.Equal(user.Email, byID.Email)
	s.Equal("Row User", byID.FullName)
	s.Equal("active", byID.Status)

	foundAccount, err := s.accounts.FindByID(ctx, account.ID)
	s.Require().NoError(err)
	s.Equal("Row Account", foundAccount.Name)
	s.Equal("active", foundAccount.Status)

	page, total, err := s.accounts.PaginateByMember(ctx, user.ID, "", "", 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), total)
	s.Equal([]uuid.UUID{account.ID}, []uuid.UUID{page[0].ID})

	members, err := s.memberships.FindByUserID(ctx, user.ID)
	s.Require().NoError(err)
	s.Len(members, 1)
	s.Equal(account.ID, members[0].AccountID)
	s.Equal("owner", members[0].Role)

	_, err = s.users.FindByEmail(ctx, "missing-"+user.Email)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	_, err = s.accounts.FindByID(ctx, uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	_, err = s.memberships.FindByAccountAndUser(ctx, account.ID, uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
}
