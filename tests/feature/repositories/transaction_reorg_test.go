package repositories_test

import (
	"context"
	"errors"

	"github.com/macrowallets/waas/app/models"
)

// A confirmation or a failure is written only to a row still open (pending or
// confirming) or, for a failure, still in the status the caller read: a row
// another process settled meanwhile is left as it is.
func (s *TransactionRepositoryTestSuite) TestRecordConfirmations_LeavesASettledRowAsItIs() {
	ctx := context.Background()
	tx := s.makeTx(s.insertWallet(), models.TxTypeDeposit, "confirming")
	s.Require().NoError(s.repo.Create(ctx, tx))
	s.Require().NoError(s.repo.MarkFailedFrom(ctx, tx.ID, models.DepositFailureReorgedOut, "confirming"))

	err := s.repo.RecordConfirmations(ctx, tx.ID, 12, "confirmed", nil)

	s.True(errors.Is(err, models.ErrTransactionSettled), "err %v", err)
	got, findErr := s.repo.FindByID(ctx, tx.ID)
	s.Require().NoError(findErr)
	s.Equal("failed", got.Status)
	s.Equal(models.DepositFailureReorgedOut, got.ErrorMessage)
}

func (s *TransactionRepositoryTestSuite) TestMarkFailedFrom_FailsOnlyFromTheStatusTheCallerRead() {
	ctx := context.Background()
	tx := s.makeTx(s.insertWallet(), models.TxTypeDeposit, "confirming")
	s.Require().NoError(s.repo.Create(ctx, tx))
	s.Require().NoError(s.repo.RecordConfirmations(ctx, tx.ID, 12, "confirmed", nil))

	err := s.repo.MarkFailedFrom(ctx, tx.ID, models.DepositFailureReorgedOut, "confirming")

	s.True(errors.Is(err, models.ErrTransactionSettled), "err %v", err)
	got, findErr := s.repo.FindByID(ctx, tx.ID)
	s.Require().NoError(findErr)
	s.Equal("confirmed", got.Status)
	// The status read is confirmed: deposits:verify-receipts fails a confirmed deposit.
	s.Require().NoError(s.repo.MarkFailedFrom(ctx, tx.ID, "onchain_reverted", "confirmed"))
	got, findErr = s.repo.FindByID(ctx, tx.ID)
	s.Require().NoError(findErr)
	s.Equal("failed", got.Status)
	s.Equal("onchain_reverted", got.ErrorMessage)
}

func (s *TransactionRepositoryTestSuite) TestUnresolvedReorgs_AreTheOpenDepositsWithAReorgMark() {
	ctx := context.Background()
	wallet := s.insertWallet()
	insert := func(chainID, txType, status, payload string) *models.Transaction {
		tx := s.makeTx(wallet, txType, status)
		tx.Chain, tx.RawPayload = chainID, payload
		s.Require().NoError(s.repo.Create(ctx, tx))
		return tx
	}
	marked := `{"reorg_seen_at":1790000000,"reorg_next_check_at":1790000060}`
	ethPending := insert("eth", models.TxTypeDeposit, "pending", marked)
	insert("eth", models.TxTypeDeposit, "confirming", marked)
	insert("btc", models.TxTypeDeposit, "confirming", marked)
	insert("eth", models.TxTypeDeposit, "confirmed", marked)
	insert("eth", models.TxTypeDeposit, "failed", marked)
	insert("eth", models.TxTypeWithdrawal, "pending", marked)
	insert("eth", models.TxTypeDeposit, "pending", `{"sender":"0xabc","sender_nonce":7}`)

	counts, err := s.repo.CountUnresolvedReorgs(ctx)
	s.Require().NoError(err)
	s.Equal(map[string]int{"eth": 2, "btc": 1}, counts)

	rows, err := s.repo.FindUnresolvedReorgs(ctx, "eth", 1)
	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	all, err := s.repo.FindUnresolvedReorgs(ctx, "", 10)
	s.Require().NoError(err)
	s.Len(all, 3)
	ethRows, err := s.repo.FindUnresolvedReorgs(ctx, "eth", 10)
	s.Require().NoError(err)
	s.Len(ethRows, 2)
	s.Contains([]string{ethRows[0].ID.String(), ethRows[1].ID.String()}, ethPending.ID.String())
}
