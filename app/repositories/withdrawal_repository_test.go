package repositories_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type WithdrawalRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.WithdrawalRepository
}

func TestWithdrawalRepositorySuite(t *testing.T) {
	suite.Run(t, new(WithdrawalRepositoryTestSuite))
}

func (s *WithdrawalRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewWithdrawalRepository(nil)
}

func (s *WithdrawalRepositoryTestSuite) insertWallet() uuid.UUID {
	w := mocks.InsertWallet(s.T(), "eth")
	return w.ID
}

func (s *WithdrawalRepositoryTestSuite) TestCreate_Success() {
	walletID := s.insertWallet()
	w := &models.Withdrawal{
		ID: uuid.New(), WalletID: walletID, Status: "pending",
		Amount: "0.001", FeeEstimate: "0", DestinationAddress: "0xdest",
	}
	err := s.repo.Create(context.Background(), w)
	s.NoError(err)
}

func (s *WithdrawalRepositoryTestSuite) TestFindByWallet_Pagination() {
	walletID := s.insertWallet()
	for i := 0; i < 5; i++ {
		s.Require().NoError(s.repo.Create(context.Background(), &models.Withdrawal{
			ID: uuid.New(), WalletID: walletID, Status: "pending",
			Amount: "0.001", FeeEstimate: "0", DestinationAddress: "0xdest",
		}))
	}

	page1, total, err := s.repo.FindByWallet(context.Background(), walletID, "", 2, 0)
	s.NoError(err)
	s.Len(page1, 2)
	s.Equal(int64(5), total)
}

func (s *WithdrawalRepositoryTestSuite) TestFindByWallet_FilterByStatus() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), &models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "pending", Amount: "0.001", FeeEstimate: "0", DestinationAddress: "0x1"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "cancelled", Amount: "0.002", FeeEstimate: "0", DestinationAddress: "0x2"}))

	pending, _, err := s.repo.FindByWallet(context.Background(), walletID, "pending", 50, 0)
	s.NoError(err)
	s.Len(pending, 1)
}

func (s *WithdrawalRepositoryTestSuite) TestFindByIDAndWallet_Found() {
	walletID := s.insertWallet()
	w := &models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "pending", Amount: "0.001", FeeEstimate: "0", DestinationAddress: "0x1"}
	s.Require().NoError(s.repo.Create(context.Background(), w))

	found, err := s.repo.FindByIDAndWallet(context.Background(), w.ID, walletID)
	s.NoError(err)
	s.NotNil(found)
}

func (s *WithdrawalRepositoryTestSuite) TestFindByIDAndWallet_WrongWallet() {
	walletID := s.insertWallet()
	w := &models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "pending", Amount: "0.001", FeeEstimate: "0", DestinationAddress: "0x1"}
	s.Require().NoError(s.repo.Create(context.Background(), w))

	otherWallet := s.insertWallet()
	found, err := s.repo.FindByIDAndWallet(context.Background(), w.ID, otherWallet)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *WithdrawalRepositoryTestSuite) TestUpdateStatus() {
	walletID := s.insertWallet()
	w := &models.Withdrawal{ID: uuid.New(), WalletID: walletID, Status: "pending", Amount: "0.001", FeeEstimate: "0", DestinationAddress: "0x1"}
	s.Require().NoError(s.repo.Create(context.Background(), w))

	err := s.repo.SetStatus(context.Background(), w.ID, "cancelled")
	s.NoError(err)

	found, err := s.repo.FindByIDAndWallet(context.Background(), w.ID, walletID)
	s.NoError(err)
	s.Equal("cancelled", found.Status)
}

func (s *WithdrawalRepositoryTestSuite) TestUpdateFieldsStoresBroadcastResult() {
	walletID := s.insertWallet()
	withdrawal := &models.Withdrawal{
		ID:                 uuid.New(),
		WalletID:           walletID,
		Status:             "broadcasting",
		Amount:             "0.001",
		FeeEstimate:        "0",
		DestinationAddress: "0x1",
	}
	s.Require().NoError(s.repo.Create(context.Background(), withdrawal))

	transactionID := uuid.New()
	s.Require().NoError(s.repo.MarkBroadcast(context.Background(), withdrawal.ID, &transactionID))

	found, err := s.repo.FindByIDAndWallet(context.Background(), withdrawal.ID, walletID)
	s.Require().NoError(err)
	s.Require().NotNil(found)
	s.Equal("broadcast", found.Status)
	s.Require().NotNil(found.TransactionID)
	s.Equal(transactionID, *found.TransactionID)
}

func (s *WithdrawalRepositoryTestSuite) TestWithinRollsBackACreate() {
	walletID := s.insertWallet()
	id := uuid.New()
	err := s.repo.Within(context.Background(), func(ctx context.Context) error {
		createErr := s.repo.Create(ctx, &models.Withdrawal{
			ID: id, WalletID: walletID, Status: "pending",
			Amount: "0.001", FeeEstimate: "0", DestinationAddress: "0xdest",
		})
		if createErr != nil {
			return createErr
		}
		return errors.New("fail the withdrawal insert")
	})
	s.Error(err)

	found, findErr := s.repo.FindByID(context.Background(), id)
	s.Nil(found)
	s.Error(findErr)
}
