package repositories_test

import (
	"bytes"
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

// nativeDeposit inserts a deposit of walletID on chainID with status and token
// contract; created moves its created_at back by that much.
func (s *TransactionRepositoryTestSuite) nativeDeposit(walletID uuid.UUID, chainID, status, tokenContract string, created time.Duration) models.Transaction {
	tx := s.makeTx(walletID, models.TxTypeDeposit, status)
	tx.Chain = chainID
	tx.TokenContract = tokenContract
	tx.BlockNumber = 100
	s.Require().NoError(s.repo.Create(context.Background(), tx))
	if created > 0 {
		_, err := facades.Orm().Query().Exec(`UPDATE transactions SET created_at = created_at - make_interval(secs => ?) WHERE id = ?`, created.Seconds(), tx.ID)
		s.Require().NoError(err)
	}
	return *tx
}

func (s *TransactionRepositoryTestSuite) TestFind_ConfirmedNativeDeposits_SelectsConfirmedNativeDepositsOfTheChainSince() {
	ctx := context.Background()
	walletID := s.insertWallet()
	recent := s.nativeDeposit(walletID, models.ChainTETH, "confirmed", "", time.Hour)
	another := s.nativeDeposit(walletID, models.ChainTETH, "confirmed", "", 2*time.Hour)
	old := s.nativeDeposit(walletID, models.ChainTETH, "confirmed", "", 40*24*time.Hour)
	s.nativeDeposit(walletID, models.ChainTETH, "confirmed", "0x1c7d4b196cb0c7b01d743fbc6116a902379c7238", time.Hour)
	s.nativeDeposit(walletID, models.ChainTETH, "confirming", "", time.Hour)
	s.nativeDeposit(walletID, models.ChainTETH, "failed", "", time.Hour)
	s.nativeDeposit(walletID, models.ChainTPolygon, "confirmed", "", time.Hour)
	withoutHash := s.nativeDeposit(walletID, models.ChainTETH, "confirmed", "", time.Hour)
	_, err := facades.Orm().Query().Exec(`UPDATE transactions SET tx_hash = '' WHERE id = ?`, withoutHash.ID)
	s.Require().NoError(err)
	nullContract := s.nativeDeposit(walletID, models.ChainTETH, "confirmed", "", time.Hour)
	_, err = facades.Orm().Query().Exec(`UPDATE transactions SET token_contract = NULL WHERE id = ?`, nullContract.ID)
	s.Require().NoError(err)
	withdrawal := s.makeTx(walletID, models.TxTypeWithdrawal, "confirmed")
	withdrawal.Chain = models.ChainTETH
	s.Require().NoError(s.repo.Create(ctx, withdrawal))

	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	rows, err := s.repo.FindConfirmedNativeDeposits(ctx, models.ChainTETH, since, uuid.Nil, 10)
	s.Require().NoError(err)
	want := []uuid.UUID{recent.ID, another.ID, nullContract.ID}
	slices.SortFunc(want, compareUUID)
	s.Equal(want, transactionIDs(rows), "confirmed native deposits of the chain recorded since, ordered by id")

	all, err := s.repo.FindConfirmedNativeDeposits(ctx, models.ChainTETH, time.Time{}, uuid.Nil, 10)
	s.Require().NoError(err)
	s.ElementsMatch(append(want, old.ID), transactionIDs(all), "a zero since reads every confirmed native deposit")
}

func (s *TransactionRepositoryTestSuite) TestFind_ConfirmedNativeDeposits_PagesAfterTheLastID() {
	ctx := context.Background()
	walletID := s.insertWallet()
	want := make([]uuid.UUID, 0, 3)
	for range 3 {
		want = append(want, s.nativeDeposit(walletID, models.ChainTETH, "confirmed", "", 0).ID)
	}
	slices.SortFunc(want, compareUUID)

	var got []uuid.UUID
	after := uuid.Nil
	for page := 0; page < 4; page++ {
		rows, err := s.repo.FindConfirmedNativeDeposits(ctx, models.ChainTETH, time.Time{}, after, 2)
		s.Require().NoError(err)
		if len(rows) == 0 {
			break
		}
		got = append(got, transactionIDs(rows)...)
		after = rows[len(rows)-1].ID
	}
	s.Equal(want, got, "pages of 2 after the last id read each deposit once, in id order")

	_, err := s.repo.FindConfirmedNativeDeposits(ctx, models.ChainTETH, time.Time{}, uuid.Nil, 0)
	s.Error(err, "a limit is required")
}

// compareUUID orders uuids the way Postgres orders the uuid type (bytewise).
func compareUUID(a, b uuid.UUID) int {
	return bytes.Compare(a[:], b[:])
}
