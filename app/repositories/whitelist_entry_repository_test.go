package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type WhitelistEntryRepositoryTestSuite struct {
	suite.Suite
	repo repositories.WhitelistEntryRepository
}

func TestWhitelistEntryRepositorySuite(t *testing.T) {
	suite.Run(t, new(WhitelistEntryRepositoryTestSuite))
}

func (s *WhitelistEntryRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewWhitelistEntryRepository()
}

func (s *WhitelistEntryRepositoryTestSuite) insertWallet() uuid.UUID {
	w := mocks.InsertWallet(s.T(), "eth")
	return w.ID
}

func (s *WhitelistEntryRepositoryTestSuite) TestCreate_Success() {
	walletID := s.insertWallet()
	entry := &models.WhitelistEntry{
		ID: uuid.New(), WalletID: walletID,
		Address: "0xabc", Label: "Cold Storage",
	}
	err := s.repo.Create(entry)
	s.NoError(err)
}

func (s *WhitelistEntryRepositoryTestSuite) TestFindByWalletID() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(&models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0x1"}))
	s.Require().NoError(s.repo.Create(&models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0x2"}))

	entries, err := s.repo.FindByWalletID(walletID)
	s.NoError(err)
	s.Len(entries, 2)
}

func (s *WhitelistEntryRepositoryTestSuite) TestFindByIDAndWallet_Found() {
	walletID := s.insertWallet()
	entry := &models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0xfind"}
	s.Require().NoError(s.repo.Create(entry))

	found, err := s.repo.FindByIDAndWallet(entry.ID, walletID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("0xfind", found.Address)
}

func (s *WhitelistEntryRepositoryTestSuite) TestFindByIDAndWallet_WrongWallet() {
	walletID := s.insertWallet()
	otherWallet := s.insertWallet()
	entry := &models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0xfind"}
	s.Require().NoError(s.repo.Create(entry))

	found, err := s.repo.FindByIDAndWallet(entry.ID, otherWallet)
	s.NoError(err)
	s.Nil(found)
}

func (s *WhitelistEntryRepositoryTestSuite) TestDelete() {
	walletID := s.insertWallet()
	entry := &models.WhitelistEntry{ID: uuid.New(), WalletID: walletID, Address: "0xdel"}
	s.Require().NoError(s.repo.Create(entry))

	err := s.repo.Delete(entry)
	s.NoError(err)

	entries, err := s.repo.FindByWalletID(walletID)
	s.NoError(err)
	s.Len(entries, 0)
}
