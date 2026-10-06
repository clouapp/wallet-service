package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type AmountGuardTestSuite struct {
	suite.Suite
	transactions *repositories.TransactionRepository
	withdrawals  *repositories.WithdrawalRepository
	balances     *repositories.WalletAssetBalanceRepository
	snapshots    *repositories.WalletBalanceSnapshotRepository
	wallets      *repositories.WalletRepository
}

func TestAmount_Guard_Suite(t *testing.T) {
	suite.Run(t, new(AmountGuardTestSuite))
}

func (s *AmountGuardTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.transactions = repositories.NewTransactionRepository(nil)
	s.withdrawals = repositories.NewWithdrawalRepository(nil)
	s.balances = repositories.NewWalletAssetBalanceRepository(nil)
	s.snapshots = repositories.NewWalletBalanceSnapshotRepository(nil)
	s.wallets = repositories.NewWalletRepository(nil)
}

func (s *AmountGuardTestSuite) solDeposit(walletID uuid.UUID, amountBaseUnits string) *models.Transaction {
	return &models.Transaction{
		ID: uuid.New(), WalletID: walletID, ExternalUserID: "user1", Chain: "sol",
		TxType: models.TxTypeDeposit, Direction: models.TxDirectionInbound, TxHash: uuid.NewString(),
		ToAddress: "So1To", Amount: amountBaseUnits, Asset: "sol", RequiredConfs: 1, Status: "confirmed",
		Source: "deposit_ingest", RawPayload: "{}",
	}
}

func (s *AmountGuardTestSuite) countTransactions(walletID uuid.UUID) int64 {
	count, err := facades.Orm().Query().Model(&models.Transaction{}).Where("wallet_id = ?", walletID).Count()
	s.Require().NoError(err)
	return count
}

func (s *AmountGuardTestSuite) TestCreate_Rejects_NegativeAmountAndFee() {
	wallet := fixtures.InsertWallet(s.T(), "sol")

	negativeAmount := s.solDeposit(wallet.ID, "-5000000000")
	s.ErrorIs(s.transactions.Create(context.Background(), negativeAmount), amount.ErrNegativeAmount)

	negativeFee := s.solDeposit(wallet.ID, "5000000000")
	negativeFee.Fee = "-5000"
	s.ErrorIs(s.transactions.Create(context.Background(), negativeFee), amount.ErrNegativeAmount)

	s.Equal(int64(0), s.countTransactions(wallet.ID), "a rejected write must not reach the database")
	s.Error(s.transactions.Create(context.Background(), nil))
}

func (s *AmountGuardTestSuite) TestCreate_Stores_TheAbsoluteAmount() {
	wallet := fixtures.InsertWallet(s.T(), "sol")
	tx := s.solDeposit(wallet.ID, "5000000000")

	s.Require().NoError(s.transactions.Create(context.Background(), tx))

	stored, err := s.transactions.FindByID(context.Background(), tx.ID)
	s.Require().NoError(err)
	s.Equal("5000000000", stored.Amount)
	s.Equal(models.TxDirectionInbound, stored.Direction)
}

func (s *AmountGuardTestSuite) TestUpdate_Fields_RejectsNegativeAmount() {
	wallet := fixtures.InsertWallet(s.T(), "sol")
	tx := s.solDeposit(wallet.ID, "5000000000")
	s.Require().NoError(s.transactions.Create(context.Background(), tx))

	s.ErrorIs(s.transactions.SetAmount(context.Background(), tx.ID, "-5000000000"), amount.ErrNegativeAmount)
	s.NoError(s.transactions.SetConfirmations(context.Background(), tx.ID, 3))

	stored, err := s.transactions.FindByID(context.Background(), tx.ID)
	s.Require().NoError(err)
	s.Equal("5000000000", stored.Amount)
}

func (s *AmountGuardTestSuite) TestWithdrawal_Rejects_NegativeAmounts() {
	wallet := fixtures.InsertWallet(s.T(), "sol")
	withdrawal := &models.Withdrawal{ID: uuid.New(), WalletID: wallet.ID, Status: models.WithdrawalStatusBroadcasting, Amount: "-0.02", FeeEstimate: "0", DestinationAddress: "So1Dest"}
	s.ErrorIs(s.withdrawals.Create(context.Background(), withdrawal), amount.ErrNegativeAmount)

	withdrawal.Amount = "0.02"
	s.Require().NoError(s.withdrawals.Create(context.Background(), withdrawal))
	s.ErrorIs(s.withdrawals.SetFeeEstimate(context.Background(), withdrawal.ID, "-0.000005"), amount.ErrNegativeAmount)
}

func (s *AmountGuardTestSuite) TestBalance_Writes_RejectNegativeAmounts() {
	wallet := fixtures.InsertWallet(s.T(), "sol")

	row := models.WalletAssetBalance{ID: uuid.New(), WalletID: wallet.ID, ChainID: "sol", AssetType: "native", AssetSymbol: "SOL", AssetKey: "SOL", Decimals: 9, AmountRaw: "-1", AmountDisplay: "-0.000000001"}
	s.ErrorIs(s.balances.ReplaceForWallet(context.Background(), wallet.ID, "sol", []models.WalletAssetBalance{row}), amount.ErrNegativeAmount)

	snapshot := &models.WalletBalanceSnapshot{ID: uuid.New(), WalletID: wallet.ID, ChainID: "sol", BalanceAsset: "SOL", BalanceRaw: "-1", BalanceDisplay: "-0.000000001"}
	s.ErrorIs(s.snapshots.Create(context.Background(), snapshot), amount.ErrNegativeAmount)

	s.ErrorIs(s.wallets.SetBalanceRaw(context.Background(), wallet.ID, "-1"), amount.ErrNegativeAmount)
}

// The CHECK constraints reject a signed amount even from writes that skip the repositories.
func (s *AmountGuardTestSuite) TestDatabase_Rejects_SignedAmountsOutsideTheRepositories() {
	wallet := fixtures.InsertWallet(s.T(), "sol")
	tx := s.solDeposit(wallet.ID, "5000000000")
	s.Require().NoError(s.transactions.Create(context.Background(), tx))
	withdrawal := &models.Withdrawal{ID: uuid.New(), WalletID: wallet.ID, Status: models.WithdrawalStatusBroadcasting, Amount: "0.02", FeeEstimate: "0", DestinationAddress: "So1Dest"}
	s.Require().NoError(s.withdrawals.Create(context.Background(), withdrawal))

	statements := map[string][]any{
		"UPDATE transactions SET amount = ? WHERE id = ?": {"-5000000000", tx.ID},
		"UPDATE transactions SET fee = ? WHERE id = ?":    {" -1", tx.ID},
		"UPDATE withdrawals SET amount = ? WHERE id = ?":  {-0.02, withdrawal.ID},
		"UPDATE wallets SET balance_raw = ? WHERE id = ?": {"-1", wallet.ID},
	}
	for statement, args := range statements {
		_, err := facades.Orm().Query().Exec(statement, args...)
		s.ErrorContains(err, "non_negative", statement)
	}
}
