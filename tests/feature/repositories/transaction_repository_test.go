package repositories_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/pkg/pgerr"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type TransactionRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.TransactionRepository
}

func TestTransaction_Repository_Suite(t *testing.T) {
	suite.Run(t, new(TransactionRepositoryTestSuite))
}

func (s *TransactionRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewTransactionRepository(nil)
}

func (s *TransactionRepositoryTestSuite) insertWallet() uuid.UUID {
	w := fixtures.InsertWallet(s.T(), "eth")
	return w.ID
}

func (s *TransactionRepositoryTestSuite) makeTx(walletID uuid.UUID, txType, status string) *models.Transaction {
	return &models.Transaction{
		ID: uuid.New(), WalletID: walletID, ExternalUserID: "user1",
		Chain: "eth", TxType: txType, TxHash: "0x" + uuid.NewString()[:16], LogIndex: types.NativeValuePosition,
		ToAddress: "0xto", Amount: "1000", Asset: "eth",
		RequiredConfs: 12, Status: status,
		Direction: testTxDirection(txType), Source: models.TxSourceChain, RawPayload: "{}",
	}
}

// testTxDirection is the direction the writers of each transaction type record.
func testTxDirection(txType string) string {
	switch txType {
	case models.TxTypeDeposit:
		return models.TxDirectionInbound
	case models.TxTypeWithdrawal:
		return models.TxDirectionOutbound
	case models.TxTypeSweep, models.TxTypeGasSeed:
		return models.TxDirectionSelf
	default:
		return models.TxDirectionUnknown
	}
}

func (s *TransactionRepositoryTestSuite) TestTransactionRepository_Create_Success() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "pending")
	err := s.repo.Create(context.Background(), tx)
	s.NoError(err)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByID_Found() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "pending")
	s.Require().NoError(s.repo.Create(context.Background(), tx))

	found, err := s.repo.FindByID(context.Background(), tx.ID)
	s.Require().NoError(err)
	s.Require().NotNil(found)
	s.Equal(tx.ID, found.ID)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByIDAndWallet_Found() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "confirmed")
	s.Require().NoError(s.repo.Create(context.Background(), tx))

	found, err := s.repo.FindByIDAndWallet(context.Background(), tx.ID.String(), walletID)
	s.Require().NoError(err)
	s.NotNil(found)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByIDAndWallet_WrongWallet() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "confirmed")
	s.Require().NoError(s.repo.Create(context.Background(), tx))

	otherWallet := s.insertWallet()
	found, err := s.repo.FindByIDAndWallet(context.Background(), tx.ID.String(), otherWallet)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByIdempotencyKey_Found() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "withdrawal", "pending")
	idemKey := "idem-key-123"
	tx.IdempotencyKey = &idemKey
	s.Require().NoError(s.repo.Create(context.Background(), tx))

	found, err := s.repo.FindByIdempotencyKey(context.Background(), "idem-key-123")
	s.Require().NoError(err)
	s.Require().NotNil(found)
	s.Equal(tx.ID, found.ID)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByIdempotencyKey_NotFound() {
	found, err := s.repo.FindByIdempotencyKey(context.Background(), "nonexistent")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByWallet_Pagination() {
	walletID := s.insertWallet()
	for i := 0; i < 5; i++ {
		s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "confirmed")))
	}

	page1, total, err := s.repo.FindByWallet(context.Background(), walletID, "", "", 2, 0)
	s.Require().NoError(err)
	s.Len(page1, 2)
	s.Equal(int64(5), total)

	page2, _, err := s.repo.FindByWallet(context.Background(), walletID, "", "", 2, 2)
	s.Require().NoError(err)
	s.Len(page2, 2)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByWallet_FilterByType() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "withdrawal", "confirmed")))

	deposits, _, err := s.repo.FindByWallet(context.Background(), walletID, "deposit", "", 50, 0)
	s.Require().NoError(err)
	s.Require().Len(deposits, 1)
	s.Equal("deposit", deposits[0].TxType)
}

func (s *TransactionRepositoryTestSuite) TestFind_ByWallet_FilterByStatus() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "pending")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "confirmed")))

	pending, _, err := s.repo.FindByWallet(context.Background(), walletID, "", "pending", 50, 0)
	s.Require().NoError(err)
	s.Len(pending, 1)
}

func (s *TransactionRepositoryTestSuite) TestCount_By_ChainAndTxHash() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "withdrawal", "confirmed")
	tx.TxHash = "0xuniquehash"
	s.Require().NoError(s.repo.Create(context.Background(), tx))

	count, err := s.repo.CountByChainAndTxHash(context.Background(), "eth", "0xuniquehash", "withdrawal")
	s.Require().NoError(err)
	s.Equal(int64(1), count)

	count, err = s.repo.CountByChainAndTxHash(context.Background(), "eth", "0xuniquehash", "sweep")
	s.Require().NoError(err)
	s.Equal(int64(0), count)
}

// deposit is a deposit row of txHash at position (log_index).
func (s *TransactionRepositoryTestSuite) deposit(walletID uuid.UUID, txHash string, position int) *models.Transaction {
	tx := s.makeTx(walletID, models.TxTypeDeposit, "pending")
	tx.TxHash = txHash
	tx.LogIndex = position
	return tx
}

func (s *TransactionRepositoryTestSuite) storedPosition(id uuid.UUID) int {
	s.T().Helper()
	found, err := s.repo.FindByID(context.Background(), id)
	s.Require().NoError(err)
	return found.LogIndex
}

// The first log of a transaction and its first output are at position 0: the
// column default (-1, the native value) must not replace it.
func (s *TransactionRepositoryTestSuite) TestCreate_Deposit_StoresPositionZero() {
	tx := s.deposit(s.insertWallet(), "0xfirstlog", 0)
	s.Require().NoError(s.repo.Create(context.Background(), tx))

	s.Equal(0, s.storedPosition(tx.ID))
	s.Equal(0, tx.LogIndex)
}

func (s *TransactionRepositoryTestSuite) TestCreate_Deposit_RefusesASecondRowAtOnePosition() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), s.deposit(walletID, "0xbatch", 3)))
	s.Require().NoError(s.repo.Create(context.Background(), s.deposit(walletID, "0xbatch", 4)), "another position of the same transaction is another deposit")

	err := s.repo.Create(context.Background(), s.deposit(walletID, "0xbatch", 3))
	s.True(pgerr.IsUniqueViolation(err), "got %v", err)
}

func (s *TransactionRepositoryTestSuite) TestFind_DepositsByChainAndTxHash_ReturnsEveryDepositOfTheTransaction() {
	ctx := context.Background()
	walletID := s.insertWallet()
	first := s.deposit(walletID, "0xbatch", 7)
	second := s.deposit(walletID, "0xbatch", 2)
	s.Require().NoError(s.repo.Create(ctx, first))
	s.Require().NoError(s.repo.Create(ctx, second))
	s.Require().NoError(s.repo.Create(ctx, s.deposit(walletID, "0xother", 2)))
	sweep := s.makeTx(walletID, models.TxTypeSweep, "pending")
	sweep.TxHash = "0xbatch"
	s.Require().NoError(s.repo.Create(ctx, sweep))
	elsewhere := s.deposit(walletID, "0xbatch", 9)
	elsewhere.Chain = "polygon"
	s.Require().NoError(s.repo.Create(ctx, elsewhere))

	found, err := s.repo.FindDepositsByChainAndTxHash(ctx, "eth", "0xbatch")
	s.Require().NoError(err)
	s.Require().Len(found, 2)
	s.Equal(second.ID, found[0].ID, "ordered by position")
	s.Equal(first.ID, found[1].ID)

	none, err := s.repo.FindDepositsByChainAndTxHash(ctx, "eth", "0xunknown")
	s.Require().NoError(err)
	s.Empty(none)
}

// lockFree takes the deposits lock of the transaction in another database
// transaction, giving up after 200 ms, and reports whether it got it.
func (s *TransactionRepositoryTestSuite) lockFree(chainID, txHash string) bool {
	s.T().Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	lockErr := errors.New("the lock was not tried")
	_ = s.repo.Within(ctx, func(txCtx context.Context) error {
		lockErr = s.repo.LockTransactionDeposits(txCtx, chainID, txHash)
		return nil
	})
	return lockErr == nil
}

func (s *TransactionRepositoryTestSuite) TestLock_TransactionDeposits_IsHeldUntilTheTransactionEnds() {
	ctx := context.Background()
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.repo.Within(ctx, func(txCtx context.Context) error {
			if err := s.repo.LockTransactionDeposits(txCtx, "eth", "0xbadc0de1"); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	s.False(s.lockFree("eth", "0xbadc0de1"), "a second recorder of the transaction waits")
	s.False(s.lockFree("eth", "0xBADC0DE1"), "a hex hash is one transaction in any case")
	s.True(s.lockFree("eth", "0xbadc0de2"), "another transaction is not held")
	s.True(s.lockFree("polygon", "0xbadc0de1"), "the same hash on another chain is not held")

	close(release)
	s.Require().NoError(<-done)
	s.True(s.lockFree("eth", "0xbadc0de1"), "the lock ends with the transaction")
}

func (s *TransactionRepositoryTestSuite) TestClaim_DepositPosition_MovesOnlyARowRecordedBeforePositions() {
	ctx := context.Background()
	walletID := s.insertWallet()
	old := s.deposit(walletID, "0xbatch", types.NativeValuePosition)
	s.Require().NoError(s.repo.Create(ctx, old))

	claimed, err := s.repo.ClaimDepositPosition(ctx, old.ID, 0)
	s.Require().NoError(err)
	s.True(claimed)
	s.Equal(0, s.storedPosition(old.ID))

	claimed, err = s.repo.ClaimDepositPosition(ctx, old.ID, 5)
	s.Require().NoError(err)
	s.False(claimed, "a row that has its position is never moved again")
	s.Equal(0, s.storedPosition(old.ID))

	withdrawal := s.makeTx(walletID, models.TxTypeWithdrawal, "pending")
	s.Require().NoError(s.repo.Create(ctx, withdrawal))
	claimed, err = s.repo.ClaimDepositPosition(ctx, withdrawal.ID, 1)
	s.Require().NoError(err)
	s.False(claimed, "only deposit rows are claimed")
	s.Equal(types.NativeValuePosition, s.storedPosition(withdrawal.ID))
}

func (s *TransactionRepositoryTestSuite) TestFill_DepositBlockHash_WritesOnlyAMissingHash() {
	ctx := context.Background()
	walletID := s.insertWallet()
	pushed := s.deposit(walletID, "0xpushed", types.NativeValuePosition)
	pushed.BlockHash = ""
	s.Require().NoError(s.repo.Create(ctx, pushed))
	scanned := s.deposit(walletID, "0xscanned", types.NativeValuePosition)
	scanned.BlockHash = "0xfirst"
	s.Require().NoError(s.repo.Create(ctx, scanned))

	s.Require().NoError(s.repo.FillDepositBlockHash(ctx, pushed.ID, "0xblock"))
	s.Require().NoError(s.repo.FillDepositBlockHash(ctx, scanned.ID, "0xother"))

	found, err := s.repo.FindByID(ctx, pushed.ID)
	s.Require().NoError(err)
	s.Equal("0xblock", found.BlockHash)
	found, err = s.repo.FindByID(ctx, scanned.ID)
	s.Require().NoError(err)
	s.Equal("0xfirst", found.BlockHash, "a hash that is there is never replaced")
}

func (s *TransactionRepositoryTestSuite) TestFind_DepositsByChainAndTxHash_MatchesAHexHashInAnyCase() {
	ctx := context.Background()
	walletID := s.insertWallet()
	upper := s.deposit(walletID, "0xABCDEF01", 3)
	s.Require().NoError(s.repo.Create(ctx, upper))
	xrp := s.deposit(walletID, "C0FFEE00C0FFEE00C0FFEE00C0FFEE00C0FFEE00C0FFEE00C0FFEE00C0FFEE00", 4)
	s.Require().NoError(s.repo.Create(ctx, xrp))
	base58 := s.deposit(walletID, "5VERv8NMvzbJ", 5)
	s.Require().NoError(s.repo.Create(ctx, base58))

	found, err := s.repo.FindDepositsByChainAndTxHash(ctx, "eth", "0xabcdef01")
	s.Require().NoError(err)
	s.Require().Len(found, 1)
	s.Equal(upper.ID, found[0].ID)

	found, err = s.repo.FindDepositsByChainAndTxHash(ctx, "eth", strings.ToLower(xrp.TxHash))
	s.Require().NoError(err)
	s.Len(found, 1)

	found, err = s.repo.FindDepositsByChainAndTxHash(ctx, "eth", "5verv8nmvzbj")
	s.Require().NoError(err)
	s.Empty(found, "a base58 signature is case-sensitive")
}

func (s *TransactionRepositoryTestSuite) TestClaim_DepositPosition_RefusesATakenPosition() {
	ctx := context.Background()
	walletID := s.insertWallet()
	old := s.deposit(walletID, "0xbatch", types.NativeValuePosition)
	s.Require().NoError(s.repo.Create(ctx, old))
	s.Require().NoError(s.repo.Create(ctx, s.deposit(walletID, "0xbatch", 3)))

	_, err := s.repo.ClaimDepositPosition(ctx, old.ID, 3)
	s.True(pgerr.IsUniqueViolation(err), "got %v", err)
	s.Equal(types.NativeValuePosition, s.storedPosition(old.ID))
}

func (s *TransactionRepositoryTestSuite) TestCount_InternalTransfers_OnlySweepsAndGasSeedsOfTheWallet() {
	walletID := s.insertWallet()
	otherWalletID := s.insertWallet()
	record := func(wallet uuid.UUID, txType, hash string) {
		tx := s.makeTx(wallet, txType, "confirming")
		tx.TxHash = hash
		s.Require().NoError(s.repo.Create(context.Background(), tx))
	}
	record(walletID, models.TxTypeSweep, "0xsweep")
	record(walletID, models.TxTypeGasSeed, "0xgasseed")
	record(walletID, models.TxTypeWithdrawal, "0xwithdrawal")
	record(otherWalletID, models.TxTypeSweep, "0xothersweep")

	cases := map[string]struct {
		chain, hash string
		want        int64
	}{
		"sweep of the wallet":         {"eth", "0xsweep", 1},
		"gas seed of the wallet":      {"eth", "0xgasseed", 1},
		"withdrawal is not internal":  {"eth", "0xwithdrawal", 0},
		"sweep of another wallet":     {"eth", "0xothersweep", 0},
		"same hash on another chain":  {"base", "0xsweep", 0},
		"hash the wallet never wrote": {"eth", "0xunknown", 0},
	}
	for name, tc := range cases {
		s.Run(name, func() {
			count, err := s.repo.CountInternalTransfers(context.Background(), tc.chain, tc.hash, walletID)
			s.Require().NoError(err)
			s.Equal(tc.want, count)
		})
	}
}

func (s *TransactionRepositoryTestSuite) TestCount_InternalTransfers_RejectsMissingKeys() {
	walletID := s.insertWallet()
	for name, args := range map[string]struct {
		chain, hash string
		wallet      uuid.UUID
	}{
		"no chain":  {"", "0xsweep", walletID},
		"no hash":   {"eth", "", walletID},
		"no wallet": {"eth", "0xsweep", uuid.Nil},
	} {
		s.Run(name, func() {
			_, err := s.repo.CountInternalTransfers(context.Background(), args.chain, args.hash, args.wallet)
			s.Error(err)
		})
	}
}

func (s *TransactionRepositoryTestSuite) TestFind_Pending_ByChain() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "pending")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "confirming")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "confirmed")))

	pending, err := s.repo.FindPendingByChain(context.Background(), "eth")
	s.Require().NoError(err)
	s.Len(pending, 2)
}

// TestFindPendingByChain_IncludesOutbound guards Fix C2: the confirmation loop
// must see sweep / withdrawal / gas_seed rows so their BlockNumber can be
// reconciled and they can reach `confirmed`. Before the fix this query was
// hard-coded to tx_type=deposit and outbound rows stayed at `confirming`
// forever.
func (s *TransactionRepositoryTestSuite) TestFind_PendingByChain_IncludesOutbound() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "pending")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "withdrawal", "confirming")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "sweep", "confirming")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "gas_seed", "confirming")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "sweep", "confirmed")))

	pending, err := s.repo.FindPendingByChain(context.Background(), "eth")
	s.Require().NoError(err)
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

func (s *TransactionRepositoryTestSuite) TestTransactionRepository_Update_Fields() {
	walletID := s.insertWallet()
	tx := s.makeTx(walletID, "deposit", "pending")
	s.Require().NoError(s.repo.Create(context.Background(), tx))

	confirmedAt := (*time.Time)(nil)
	err := s.repo.RecordConfirmations(context.Background(), tx.ID, 5, "confirming", confirmedAt)
	s.Require().NoError(err)

	found, err := s.repo.FindByID(context.Background(), tx.ID)
	s.Require().NoError(err)
	s.Equal(5, found.Confirmations)
	s.Equal("confirming", found.Status)
}

func (s *TransactionRepositoryTestSuite) TestList_Global_Filters() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTx(walletID, "withdrawal", "pending")))

	all, _, err := s.repo.List(context.Background(), "eth", "", "", "", 50, 0)
	s.Require().NoError(err)
	s.Len(all, 2)

	deposits, _, err := s.repo.List(context.Background(), "eth", "deposit", "", "", 50, 0)
	s.Require().NoError(err)
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
func (s *TransactionRepositoryTestSuite) TestList_ForAccount_FiltersByAccount() {
	accountA := fixtures.InsertAccount(s.T(), "acc-A")
	accountB := fixtures.InsertAccount(s.T(), "acc-B")

	walletA := fixtures.InsertWalletWithAccount(s.T(), "eth", &accountA.ID)
	walletB := fixtures.InsertWalletWithAccount(s.T(), "eth", &accountB.ID)

	s.Require().NoError(s.repo.Create(context.Background(), s.makeTxForUser(walletA.ID, "shared_user", "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTxForUser(walletA.ID, "shared_user", "withdrawal", "pending")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTxForUser(walletB.ID, "shared_user", "deposit", "confirmed")))

	txsA, totalA, err := s.repo.ListForAccount(context.Background(), accountA.ID, "", "", "", "shared_user", 50, 0)
	s.Require().NoError(err)
	s.Equal(int64(2), totalA)
	s.Len(txsA, 2)
	for _, tx := range txsA {
		s.Equal(walletA.ID, tx.WalletID, "account A must only see wallet A's transactions")
	}

	txsB, totalB, err := s.repo.ListForAccount(context.Background(), accountB.ID, "", "", "", "shared_user", 50, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), totalB)
	s.Require().Len(txsB, 1)
	s.Equal(walletB.ID, txsB[0].WalletID)
}

// TestListForAccount_ExcludesUnassignedWallets mirrors the address case:
// transactions on wallets with NULL account_id (legacy data) must never leak
// into any account's scoped view.
func (s *TransactionRepositoryTestSuite) TestList_ForAccount_ExcludesUnassignedWallets() {
	account := fixtures.InsertAccount(s.T(), "acc-scoped")
	unassigned := fixtures.InsertWallet(s.T(), "eth") // account_id = NULL
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTxForUser(unassigned.ID, "user_x", "deposit", "confirmed")))

	txs, total, err := s.repo.ListForAccount(context.Background(), account.ID, "", "", "", "user_x", 50, 0)
	s.Require().NoError(err)
	s.Equal(int64(0), total)
	s.Empty(txs)
}

// TestListForAccount_AppliesSecondaryFilters confirms chain/type/status filters
// are still honored in addition to the account-level filter.
func (s *TransactionRepositoryTestSuite) TestList_ForAccount_AppliesSecondaryFilters() {
	account := fixtures.InsertAccount(s.T(), "acc")
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &account.ID)

	s.Require().NoError(s.repo.Create(context.Background(), s.makeTxForUser(wallet.ID, "u", "deposit", "confirmed")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTxForUser(wallet.ID, "u", "withdrawal", "pending")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeTxForUser(wallet.ID, "u", "deposit", "pending")))

	deposits, _, err := s.repo.ListForAccount(context.Background(), account.ID, "eth", "deposit", "", "u", 50, 0)
	s.Require().NoError(err)
	s.Len(deposits, 2)

	pendingWithdrawals, _, err := s.repo.ListForAccount(context.Background(), account.ID, "eth", "withdrawal", "pending", "u", 50, 0)
	s.Require().NoError(err)
	s.Len(pendingWithdrawals, 1)
}

func (s *TransactionRepositoryTestSuite) TestWithin_Commits_ASweepLegAndItsWebhook() {
	walletID := s.insertWallet()
	txID := uuid.New()
	eventID := uuid.New()

	err := s.repo.Within(context.Background(), func(ctx context.Context) error {
		tx := s.makeTx(walletID, models.TxTypeSweep, "confirming")
		tx.ID = txID
		if createErr := s.repo.Create(ctx, tx); createErr != nil {
			return createErr
		}
		return repositories.NewWebhookEventRepository(nil).Create(ctx, sweepBroadcastEvent(eventID, txID))
	})
	s.Require().NoError(err)

	found, findErr := s.repo.FindByID(context.Background(), txID)
	s.Require().NoError(findErr)
	s.Require().NotNil(found)
	s.Equal(txID, found.ID)
	s.Equal(int64(1), s.countWebhookEvents(eventID))
}

func (s *TransactionRepositoryTestSuite) TestWithin_Rolls_BackASweepLegAndItsWebhook() {
	walletID := s.insertWallet()
	txID := uuid.New()
	eventID := uuid.New()

	err := s.repo.Within(context.Background(), func(ctx context.Context) error {
		tx := s.makeTx(walletID, models.TxTypeSweep, "confirming")
		tx.ID = txID
		if createErr := s.repo.Create(ctx, tx); createErr != nil {
			return createErr
		}
		if createErr := repositories.NewWebhookEventRepository(nil).Create(ctx, sweepBroadcastEvent(eventID, txID)); createErr != nil {
			return createErr
		}
		return errors.New("fail the sweep leg")
	})
	s.Error(err)

	found, findErr := s.repo.FindByID(context.Background(), txID)
	s.Nil(found)
	s.Error(findErr)
	s.Equal(int64(0), s.countWebhookEvents(eventID))
}

func sweepBroadcastEvent(eventID, txID uuid.UUID) *models.WebhookEvent {
	return &models.WebhookEvent{
		ID:             eventID,
		TransactionID:  &txID,
		EventType:      "sweep.broadcast",
		Payload:        "{}",
		DeliveryURL:    "https://example.test/hooks",
		DeliveryStatus: "pending",
	}
}

func (s *TransactionRepositoryTestSuite) countWebhookEvents(eventID uuid.UUID) int64 {
	s.T().Helper()
	count, err := facades.Orm().Query().Model(&models.WebhookEvent{}).Where("id = ?", eventID).Count()
	s.Require().NoError(err)
	return count
}
