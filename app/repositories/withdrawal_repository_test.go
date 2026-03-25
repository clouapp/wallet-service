package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type WithdrawalRepositoryTestSuite struct {
	suite.Suite
	repo repositories.WithdrawalRepository
}

func TestWithdrawalRepositorySuite(t *testing.T) {
	suite.Run(t, new(WithdrawalRepositoryTestSuite))
}

func (s *WithdrawalRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewWithdrawalRepository()
}

func (s *WithdrawalRepositoryTestSuite) insertWallet() uuid.UUID {
	w := mocks.InsertWallet(s.T(), "eth")
	return w.ID
}

func (s *WithdrawalRepositoryTestSuite) TestCreate_Success() {
	walletID := s.insertWallet()
	w := &models.Withdrawal{
		ID: uuid.New(), WalletID: walletID, Status: "pending",
		Amount: "0.001", DestinationAddress: "0xdest",
	}
	err := s.repo.Create(w)
	s.NoError(err)
}

func (s *WithdrawalRepositoryTestSuite) TestFindByWallet_Pagination() {
	walletID := s.insertWallet()
	for i := 0; i < 5; i++ {
		s.Require().NoError(s.repo.Create(&models.Withdrawal{
			ID: uuid.New(), WalletID: walletID, Status: "pending",
			Amount: "0.001", DestinationAddress: "0xdest",
		}))
	}

	page1, err := s.repo.FindByWallet(walletID, "", 2, 0)
	s.NoError(err)
	s.Len(page1, 2)
}

func (s *WithdrawalRepositoryTestSuite) TestFindByWallet_FilterByStatus() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(&models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "pending", Amount: "0.001", DestinationAddress: "0x1"}))
	s.Require().NoError(s.repo.Create(&models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "cancelled", Amount: "0.002", DestinationAddress: "0x2"}))

	pending, err := s.repo.FindByWallet(walletID, "pending", 50, 0)
	s.NoError(err)
	s.Len(pending, 1)
}

func (s *WithdrawalRepositoryTestSuite) TestFindByIDAndWallet_Found() {
	walletID := s.insertWallet()
	w := &models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "pending", Amount: "0.001", DestinationAddress: "0x1"}
	s.Require().NoError(s.repo.Create(w))

	found, err := s.repo.FindByIDAndWallet(w.ID, walletID)
	s.NoError(err)
	s.NotNil(found)
}

func (s *WithdrawalRepositoryTestSuite) TestFindByIDAndWallet_WrongWallet() {
	walletID := s.insertWallet()
	w := &models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "pending", Amount: "0.001", DestinationAddress: "0x1"}
	s.Require().NoError(s.repo.Create(w))

	otherWallet := s.insertWallet()
	found, err := s.repo.FindByIDAndWallet(w.ID, otherWallet)
	s.NoError(err)
	s.Nil(found)
}

func (s *WithdrawalRepositoryTestSuite) TestUpdateStatus() {
	walletID := s.insertWallet()
	w := &models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "pending", Amount: "0.001", DestinationAddress: "0x1"}
	s.Require().NoError(s.repo.Create(w))

	err := s.repo.UpdateStatus(w.ID, "cancelled")
	s.NoError(err)

	found, err := s.repo.FindByIDAndWallet(w.ID, walletID)
	s.NoError(err)
	s.Equal("cancelled", found.Status)
}
