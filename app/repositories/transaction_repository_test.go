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
	idemKey := "idem-key-123"
	tx.IdempotencyKey = &idemKey
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

	page1, total, err := s.repo.FindByWallet(walletID, "", "", 2, 0)
	s.NoError(err)
	s.Len(page1, 2)
	s.Equal(int64(5), total)

	page2, _, err := s.repo.FindByWallet(walletID, "", "", 2, 2)
	s.NoError(err)
	s.Len(page2, 2)
}

func (s *TransactionRepositoryTestSuite) TestFindByWallet_FilterByType() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "withdrawal", "confirmed")))

	deposits, _, err := s.repo.FindByWallet(walletID, "deposit", "", 50, 0)
	s.NoError(err)
	s.Len(deposits, 1)
	s.Equal("deposit", deposits[0].TxType)
}

func (s *TransactionRepositoryTestSuite) TestFindByWallet_FilterByStatus() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "pending")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "confirmed")))

	pending, _, err := s.repo.FindByWallet(walletID, "", "pending", 50, 0)
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

// TestFindPendingByChain_IncludesOutbound guards Fix C2: the confirmation loop
// must see sweep / withdrawal / gas_seed rows so their BlockNumber can be
// reconciled and they can reach `confirmed`. Before the fix this query was
// hard-coded to tx_type=deposit and outbound rows stayed at `confirming`
// forever.
func (s *TransactionRepositoryTestSuite) TestFindPendingByChain_IncludesOutbound() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "deposit", "pending")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "withdrawal", "confirming")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "sweep", "confirming")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "gas_seed", "confirming")))
	s.Require().NoError(s.repo.Create(s.makeTx(walletID, "sweep", "confirmed")))

	pending, err := s.repo.FindPendingByChain("eth")
	s.NoError(err)
	s.Len(pending, 4)

	byType := map[string]int{}
	for _, tx := range pending {
		byType[tx.TxType]++
	}
	s.Equal(1, byType["deposit"])
	s.Equal(1, byType["withdrawal"])
	s.Equal(1, byType["sweep"])
	s.Equal(1, byType["gas_seed"])
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

	all, _, err := s.repo.List("eth", "", "", "", 50, 0)
	s.NoError(err)
	s.Len(all, 2)

	deposits, _, err := s.repo.List("eth", "deposit", "", "", 50, 0)
	s.NoError(err)
	s.Len(deposits, 1)
}

// makeTxForUser is a variant of makeTx that lets the test set ExternalUserID
// so we can exercise the user_id filter in ListForAccount.
func (s *TransactionRepositoryTestSuite) makeTxForUser(walletID uuid.UUID, userID, txType, status string) *models.Transaction {
	tx := s.makeTx(walletID, txType, status)
	tx.ExternalUserID = userID
	return tx
}

// TestListForAccount_FiltersByAccount guards the IDOR fix on
// GET /api/v1/users/{external_id}/transactions. Two accounts each have a
// transaction tagged external_user_id="shared_user"; a query issued with
// accountA.ID must never see accountB's row.
func (s *TransactionRepositoryTestSuite) TestListForAccount_FiltersByAccount() {
	accountA := mocks.InsertAccount(s.T(), "acc-A")
	accountB := mocks.InsertAccount(s.T(), "acc-B")

	walletA := mocks.InsertWalletWithAccount(s.T(), "eth", &accountA.ID)
	walletB := mocks.InsertWalletWithAccount(s.T(), "eth", &accountB.ID)

	s.Require().NoError(s.repo.Create(s.makeTxForUser(walletA.ID, "shared_user", "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(s.makeTxForUser(walletA.ID, "shared_user", "withdrawal", "pending")))
	s.Require().NoError(s.repo.Create(s.makeTxForUser(walletB.ID, "shared_user", "deposit", "confirmed")))

	txsA, totalA, err := s.repo.ListForAccount(accountA.ID, "", "", "", "shared_user", 50, 0)
	s.NoError(err)
	s.Equal(int64(2), totalA)
	s.Len(txsA, 2)
	for _, tx := range txsA {
		s.Equal(walletA.ID, tx.WalletID, "account A must only see wallet A's transactions")
	}

	txsB, totalB, err := s.repo.ListForAccount(accountB.ID, "", "", "", "shared_user", 50, 0)
	s.NoError(err)
	s.Equal(int64(1), totalB)
	s.Len(txsB, 1)
	s.Equal(walletB.ID, txsB[0].WalletID)
}

// TestListForAccount_ExcludesUnassignedWallets mirrors the address case:
// transactions on wallets with NULL account_id (legacy data) must never leak
// into any account's scoped view.
func (s *TransactionRepositoryTestSuite) TestListForAccount_ExcludesUnassignedWallets() {
	account := mocks.InsertAccount(s.T(), "acc-scoped")
	unassigned := mocks.InsertWallet(s.T(), "eth") // account_id = NULL
	s.Require().NoError(s.repo.Create(s.makeTxForUser(unassigned.ID, "user_x", "deposit", "confirmed")))

	txs, total, err := s.repo.ListForAccount(account.ID, "", "", "", "user_x", 50, 0)
	s.NoError(err)
	s.Equal(int64(0), total)
	s.Empty(txs)
}

// TestListForAccount_AppliesSecondaryFilters confirms chain/type/status filters
// are still honored in addition to the account-level filter.
func (s *TransactionRepositoryTestSuite) TestListForAccount_AppliesSecondaryFilters() {
	account := mocks.InsertAccount(s.T(), "acc")
	wallet := mocks.InsertWalletWithAccount(s.T(), "eth", &account.ID)

	s.Require().NoError(s.repo.Create(s.makeTxForUser(wallet.ID, "u", "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(s.makeTxForUser(wallet.ID, "u", "withdrawal", "pending")))
	s.Require().NoError(s.repo.Create(s.makeTxForUser(wallet.ID, "u", "deposit", "pending")))

	deposits, _, err := s.repo.ListForAccount(account.ID, "eth", "deposit", "", "u", 50, 0)
	s.NoError(err)
	s.Len(deposits, 2)

	pendingWithdrawals, _, err := s.repo.ListForAccount(account.ID, "eth", "withdrawal", "pending", "u", 50, 0)
	s.NoError(err)
	s.Len(pendingWithdrawals, 1)
}
