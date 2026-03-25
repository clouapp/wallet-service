package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type AccountRepositoryTestSuite struct {
	suite.Suite
	repo repositories.AccountRepository
}

func TestAccountRepositorySuite(t *testing.T) {
	suite.Run(t, new(AccountRepositoryTestSuite))
}

func (s *AccountRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewAccountRepository()
}

func (s *AccountRepositoryTestSuite) TestCreate_Success() {
	acc := &models.Account{ID: uuid.New(), Name: "Test Account", Status: "active"}
	err := s.repo.Create(acc)
	s.NoError(err)

	found, err := s.repo.FindByID(acc.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("Test Account", found.Name)
}

func (s *AccountRepositoryTestSuite) TestFindByID_Found() {
	acc := &models.Account{ID: uuid.New(), Name: "Find Me", Status: "active"}
	s.Require().NoError(s.repo.Create(acc))

	found, err := s.repo.FindByID(acc.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal(acc.ID, found.ID)
}

func (s *AccountRepositoryTestSuite) TestFindByID_NotFound() {
	found, err := s.repo.FindByID(uuid.New())
	s.NoError(err)
	s.Nil(found)
}

func (s *AccountRepositoryTestSuite) TestFindByIDs() {
	a1 := &models.Account{ID: uuid.New(), Name: "A1", Status: "active"}
	a2 := &models.Account{ID: uuid.New(), Name: "A2", Status: "active"}
	a3 := &models.Account{ID: uuid.New(), Name: "A3", Status: "active"}
	s.Require().NoError(s.repo.Create(a1))
	s.Require().NoError(s.repo.Create(a2))
	s.Require().NoError(s.repo.Create(a3))

	results, err := s.repo.FindByIDs([]uuid.UUID{a1.ID, a3.ID})
	s.NoError(err)
	s.Len(results, 2)
}

func (s *AccountRepositoryTestSuite) TestFindByIDs_Empty() {
	results, err := s.repo.FindByIDs([]uuid.UUID{})
	s.NoError(err)
	s.Len(results, 0)
}

func (s *AccountRepositoryTestSuite) TestUpdateField() {
	acc := &models.Account{ID: uuid.New(), Name: "Old Name", Status: "active"}
	s.Require().NoError(s.repo.Create(acc))

	err := s.repo.UpdateField(acc.ID, "name", "New Name")
	s.NoError(err)

	found, err := s.repo.FindByID(acc.ID)
	s.NoError(err)
	s.Equal("New Name", found.Name)
}
