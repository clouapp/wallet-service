package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type TransactionRepositoryTestSuite struct {
	suite.Suite
	repo repositories.TransactionRepository
}

func TestTransactionRepositorySuite(t *testing.T) {
	suite.Run(t, new(TransactionRepositoryTestSuite))
}

func (s *TransactionRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewTransactionRepository()
}

func (s *TransactionRepositoryTestSuite) insertWallet() uuid.UUID {
	w := mocks.InsertWallet(s.T(), "eth")
	return w.ID
}

func (s *TransactionRepositoryTestSuite) makeTx(walletID uuid.UUID, txType, status string) *models.Transaction {
	return &models.Transaction{
		ID: uuid.New(), WalletID: walletID, ExternalUserID: "user1",
		Chain: "eth", TxType: txType, TxHash: "0x" + uuid.NewString()[:16],
		ToAddress: "0xto", Amount: "1000", Asset: "eth",
		RequiredConfs: 12, Status: status,
	}
}

func (s *TransactionRepositoryTestSuite) TestCreate_Success() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "pending")
	err := s.repo.Create(tx)
	s.NoError(err)
}

func (s *TransactionRepositoryTestSuite) TestFindByID_Found() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "pending")
	s.Require().NoError(s.repo.Create(tx))

	found, err := s.repo.FindByID(tx.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal(tx.ID, found.ID)
}

func (s *TransactionRepositoryTestSuite) TestFindByID_NotFound() {
	found, err := s.repo.FindByID(uuid.New())
	s.NoError(err)
	s.Nil(found)
}

func (s *TransactionRepositoryTestSuite) TestFindByIDAndWallet_Found() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "confirmed")
	s.Require().NoError(s.repo.Create(tx))

	found, err := s.repo.FindByIDAndWallet(tx.ID.String(), walletID)
	s.NoError(err)
	s.NotNil(found)
}

func (s *TransactionRepositoryTestSuite) TestFindByIDAndWallet_WrongWallet() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "confirmed")
	s.Require().NoError(s.repo.Create(tx))

	otherWallet := s.insertWallet()
	found, err := s.repo.FindByIDAndWallet(tx.ID.String(), otherWallet)
	s.NoError(err)
	s.Nil(found)
}

func (s *TransactionRepositoryTestSuite) TestFindByIdempotencyKey_Found() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "withdrawal", "pending")
	tx.IdempotencyKey = "idem-key-123"
	s.Require().NoError(s.repo.Create(tx))

	found, err := s.repo.FindByIdempotencyKey("idem-key-123")
	s.NoError(err)
	s.NotNil(found)
	s.Equal(tx.ID, found.ID)
}

func (s *TransactionRepositoryTestSuite) TestFindByIdempotencyKey_NotFound() {
	found, err := s.repo.FindByIdempotencyKey("nonexistent")
	s.NoError(err)
	s.Nil(found)
}

func (s *TransactionRepositoryTestSuite) TestFindByWallet_Pagination() {
	walletID := s.insertWallet()
	for i := 0; i < 5; i++ {
		s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "confirmed")))
	}

	page1, err := s.repo.FindByWallet(walletID, "", "", 2, 0)
	s.NoError(err)
	s.Len(page1, 2)

	page2, err := s.repo.FindByWallet(walletID, "", "", 2, 2)
	s.NoError(err)
	s.Len(page2, 2)
}

func (s *TransactionRepositoryTestSuite) TestFindByWallet_FilterByType() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "withdrawal", "confirmed")))

	deposits, err := s.repo.FindByWallet(walletID, "deposit", "", 50, 0)
	s.NoError(err)
	s.Len(deposits, 1)
	s.Equal("deposit", deposits[0].TxType)
}

func (s *TransactionRepositoryTestSuite) TestFindByWallet_FilterByStatus() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "pending")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "confirmed")))

	pending, err := s.repo.FindByWallet(walletID, "", "pending", 50, 0)
	s.NoError(err)
	s.Len(pending, 1)
}

func (s *TransactionRepositoryTestSuite) TestCountByChainAndTxHash() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "confirmed")
	tx.TxHash = "0xuniquehash"
	s.Require().NoError(s.repo.Create(tx))

	count, err := s.repo.CountByChainAndTxHash("eth", "0xuniquehash", "deposit")
	s.NoError(err)
	s.Equal(int64(1), count)

	count, err = s.repo.CountByChainAndTxHash("eth", "0xuniquehash", "withdrawal")
	s.NoError(err)
	s.Equal(int64(0), count)
}

func (s *TransactionRepositoryTestSuite) TestFindPendingByChain() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "pending")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "confirming")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "confirmed")))

	pending, err := s.repo.FindPendingByChain("eth")
	s.NoError(err)
	s.Len(pending, 2)
}

func (s *TransactionRepositoryTestSuite) TestUpdateFields() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "pending")
	s.Require().NoError(s.repo.Create(tx))

	err := s.repo.UpdateFields(tx.ID, map[string]interface{}{
		"confirmations": 5,
		"status":        "confirming",
	})
	s.NoError(err)

	found, err := s.repo.FindByID(tx.ID)
	s.NoError(err)
	s.Equal(5, found.Confirmations)
	s.Equal("confirming", found.Status)
}

func (s *TransactionRepositoryTestSuite) TestList_GlobalFilters() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "withdrawal", "pending")))

	all, err := s.repo.List("eth", "", "", "", 50, 0)
	s.NoError(err)
	s.Len(all, 2)

	deposits, err := s.repo.List("eth", "deposit", "", "", 50, 0)
	s.NoError(err)
	s.Len(deposits, 1)
}
