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

type WhitelistEntryRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.WhitelistEntryRepository
}

func TestWhitelist_Entry_RepositorySuite(t *testing.T) {
	suite.Run(t, new(WhitelistEntryRepositoryTestSuite))
}

func (s *WhitelistEntryRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewWhitelistEntryRepository(nil)
}

func (s *WhitelistEntryRepositoryTestSuite) insertWallet() uuid.UUID {
	w := fixtures.InsertWallet(s.T(), "eth")
	return w.ID
}

func (s *WhitelistEntryRepositoryTestSuite) TestWhitelistEntryRepository_Create_Success() {
	walletID := s.insertWallet()
	entry := &models.WhitelistEntry{
		ID: uuid.New(), WalletID: walletID,
		Address: "0xabc", Label: "Cold Storage",
	}
	err := s.repo.Create(context.Background(), entry)
	s.NoError(err)
}

func (s *WhitelistEntryRepositoryTestSuite) TestFind_By_WalletID() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), &models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0x1"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0x2"}))

	entries, err := s.repo.FindByWalletID(context.Background(), walletID)
	s.NoError(err)
	s.Len(entries, 2)
}

func (s *WhitelistEntryRepositoryTestSuite) TestFind_ByIDAndWallet_Found() {
	walletID := s.insertWallet()
	entry := &models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0xfind"}
	s.Require().NoError(s.repo.Create(context.Background(), entry))

	found, err := s.repo.FindByIDAndWallet(context.Background(), entry.ID, walletID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("0xfind", found.Address)
}

func (s *WhitelistEntryRepositoryTestSuite) TestFind_ByIDAndWallet_WrongWallet() {
	walletID := s.insertWallet()
	otherWallet := s.insertWallet()
	entry := &models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0xfind"}
	s.Require().NoError(s.repo.Create(context.Background(), entry))

	found, err := s.repo.FindByIDAndWallet(context.Background(), entry.ID, otherWallet)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *WhitelistEntryRepositoryTestSuite) TestWhitelistEntryRepository_Delete_Succeeds() {
	walletID := s.insertWallet()
	entry := &models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0xdel"}
	s.Require().NoError(s.repo.Create(context.Background(), entry))

	err := s.repo.Delete(context.Background(), entry)
	s.NoError(err)

	entries, err := s.repo.FindByWalletID(context.Background(), walletID)
	s.NoError(err)
	s.Len(entries, 0)
}
