package repositories_test

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// stampUpdatedAt sets transactions.updated_at directly, so a test controls the
// keyset order the changes feed reads.
func (s *TransactionRepositoryTestSuite) stampUpdatedAt(id uuid.UUID, at time.Time) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(`UPDATE transactions SET updated_at = ? WHERE id = ?`, at.UTC(), id)
	s.Require().NoError(err)
}

// updatedAt reads transactions.updated_at back in UTC.
func (s *TransactionRepositoryTestSuite) updatedAt(id uuid.UUID) time.Time {
	s.T().Helper()
	found, err := s.repo.FindByID(context.Background(), id)
	s.Require().NoError(err)
	s.Require().NotNil(found.UpdatedAt)
	return found.UpdatedAt.StdTime().UTC()
}

// Every write the confirmation tracker and the withdrawal flow make moves
// updated_at, so the changes feed returns the row again after each of them.
func (s *TransactionRepositoryTestSuite) TestUpdates_Move_UpdatedAt() {
	ctx := context.Background()
	confirmedAt := time.Now().UTC()
	writes := map[string]func(id uuid.UUID) error{
		"confirming": func(id uuid.UUID) error {
			return s.repo.RecordConfirmations(ctx, id, 3, "confirming", nil)
		},
		"confirmed": func(id uuid.UUID) error {
			return s.repo.RecordConfirmations(ctx, id, 12, "confirmed", &confirmedAt)
		},
		"confirmation count": func(id uuid.UUID) error { return s.repo.SetConfirmations(ctx, id, 4) },
		"failed":             func(id uuid.UUID) error { return s.repo.MarkFailed(ctx, id, "expired") },
		"block number":       func(id uuid.UUID) error { return s.repo.SetBlockNumber(ctx, id, 123) },
		"fee":                func(id uuid.UUID) error { return s.repo.SetFee(ctx, id, "21000") },
		"broadcast": func(id uuid.UUID) error {
			return s.repo.RecordBroadcast(ctx, id, "0xnodehash", `{"broadcast_outcome":"unknown"}`)
		},
	}
	stale := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for name, write := range writes {
		s.Run(name, func() {
			tx := s.makeTx(s.insertWallet(), models.TxTypeDeposit, "pending")
			s.Require().NoError(s.repo.Create(ctx, tx))
			s.stampUpdatedAt(tx.ID, stale)
			s.Require().Equal(stale, s.updatedAt(tx.ID))

			before := time.Now().UTC().Add(-2 * time.Second)
			s.Require().NoError(write(tx.ID))

			got := s.updatedAt(tx.ID)
			s.True(got.After(before), "updated_at = %s, want it moved to the time of the write (after %s)", got, before)
			s.True(got.Before(time.Now().UTC().Add(2*time.Second)), "updated_at = %s is in the future", got)
		})
	}
}

// changesFixture is one account with five transactions on two updated_at
// values plus an earlier one, and a second account with its own row.
type changesFixture struct {
	accountID uuid.UUID
	walletID  uuid.UUID
	t0, t1    time.Time
	// ordered holds the account's transactions in (updated_at, id) order.
	ordered []*models.Transaction
}

func (s *TransactionRepositoryTestSuite) seedChanges() changesFixture {
	s.T().Helper()
	ctx := context.Background()
	account := fixtures.InsertAccount(s.T(), "changes")
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &account.ID)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)

	// Ids chosen so the tie on t1 has to be broken by id: ...02 < ...03 < ...04.
	rows := []struct {
		id     string
		at     time.Time
		txType string
		status string
		user   string
	}{
		{"00000000-0000-4000-8000-000000000004", t1, models.TxTypeDeposit, "confirmed", "alice"},
		{"00000000-0000-4000-8000-000000000001", t0, models.TxTypeDeposit, "pending", "alice"},
		{"00000000-0000-4000-8000-000000000003", t1, models.TxTypeWithdrawal, "confirmed", "bob"},
		{"00000000-0000-4000-8000-000000000002", t1, models.TxTypeDeposit, "confirmed", "bob"},
	}
	byID := map[string]*models.Transaction{}
	for _, row := range rows {
		tx := s.makeTxForUser(wallet.ID, row.user, row.txType, row.status)
		tx.ID = uuid.MustParse(row.id)
		s.Require().NoError(s.repo.Create(ctx, tx))
		s.stampUpdatedAt(tx.ID, row.at)
		byID[row.id] = tx
	}

	other := fixtures.InsertAccount(s.T(), "changes-other")
	otherWallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &other.ID)
	foreign := s.makeTxForUser(otherWallet.ID, "alice", models.TxTypeDeposit, "confirmed")
	s.Require().NoError(s.repo.Create(ctx, foreign))
	s.stampUpdatedAt(foreign.ID, t1)

	unassigned := s.makeTxForUser(s.insertWallet(), "alice", models.TxTypeDeposit, "confirmed")
	s.Require().NoError(s.repo.Create(ctx, unassigned))
	s.stampUpdatedAt(unassigned.ID, t1)

	return changesFixture{
		accountID: account.ID,
		walletID:  wallet.ID,
		t0:        t0,
		t1:        t1,
		ordered: []*models.Transaction{
			byID["00000000-0000-4000-8000-000000000001"],
			byID["00000000-0000-4000-8000-000000000002"],
			byID["00000000-0000-4000-8000-000000000003"],
			byID["00000000-0000-4000-8000-000000000004"],
		},
	}
}

func ids(rows []models.Transaction) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i := range rows {
		out[i] = rows[i].ID
	}
	return out
}

func idsOf(rows []*models.Transaction) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i := range rows {
		out[i] = rows[i].ID
	}
	return out
}

// Walking the feed page by page from the start returns every transaction of the
// account once, in (updated_at, id) order, ties on updated_at included.
func (s *TransactionRepositoryTestSuite) TestList_ChangedForAccount_WalksTheKeysetInOrder() {
	fixture := s.seedChanges()
	ctx := context.Background()

	var walked []uuid.UUID
	afterAt, afterID := time.Time{}, uuid.Nil
	totals := []int64{}
	for page := 0; page < 10; page++ {
		rows, total, err := s.repo.ListChangedForAccount(ctx, fixture.accountID, "", "", "", "", afterAt, afterID, 3)
		s.Require().NoError(err)
		totals = append(totals, total)
		if len(rows) == 0 {
			break
		}
		walked = append(walked, ids(rows)...)
		last := rows[len(rows)-1]
		afterAt, afterID = last.UpdatedAt.StdTime().UTC(), last.ID
	}

	s.Equal(idsOf(fixture.ordered), walked)
	s.Equal([]int64{4, 1, 0}, totals, "total counts the rows from the page's position on")
}

func (s *TransactionRepositoryTestSuite) TestList_ChangedForAccount_StartsStrictlyAfterThePosition() {
	fixture := s.seedChanges()
	ctx := context.Background()

	cases := map[string]struct {
		afterAt time.Time
		afterID uuid.UUID
		want    []*models.Transaction
	}{
		"inclusive start at t1 with the nil id": {fixture.t1, uuid.Nil, fixture.ordered[1:]},
		"inside the tie on t1":                  {fixture.t1, fixture.ordered[1].ID, fixture.ordered[2:]},
		"the last row":                          {fixture.t1, fixture.ordered[3].ID, nil},
		"between t0 and t1":                     {fixture.t0.Add(time.Second), uuid.Nil, fixture.ordered[1:]},
		"a sub-second instant after t0":         {fixture.t0.Add(500 * time.Millisecond), uuid.Nil, fixture.ordered[1:]},
		"before everything":                     {fixture.t0.Add(-time.Hour), uuid.Nil, fixture.ordered},
	}
	for name, tc := range cases {
		s.Run(name, func() {
			rows, total, err := s.repo.ListChangedForAccount(ctx, fixture.accountID, "", "", "", "", tc.afterAt, tc.afterID, 50)
			s.Require().NoError(err)
			s.Equal(idsOf(tc.want), ids(rows))
			s.Equal(int64(len(tc.want)), total)
		})
	}
}

func (s *TransactionRepositoryTestSuite) TestList_ChangedForAccount_AppliesTheFilters() {
	fixture := s.seedChanges()
	ctx := context.Background()

	deposits, total, err := s.repo.ListChangedForAccount(ctx, fixture.accountID, "eth", models.TxTypeDeposit, "confirmed", "", fixture.t0, uuid.Nil, 50)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{fixture.ordered[1].ID, fixture.ordered[3].ID}, ids(deposits))
	s.Equal(int64(2), total)

	afterFirst, total, err := s.repo.ListChangedForAccount(ctx, fixture.accountID, "", models.TxTypeDeposit, "", "", fixture.t1, fixture.ordered[1].ID, 50)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{fixture.ordered[3].ID}, ids(afterFirst))
	s.Equal(int64(1), total)

	bob, _, err := s.repo.ListChangedForAccount(ctx, fixture.accountID, "", "", "", "bob", time.Time{}, uuid.Nil, 50)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{fixture.ordered[1].ID, fixture.ordered[2].ID}, ids(bob))

	otherChain, total, err := s.repo.ListChangedForAccount(ctx, fixture.accountID, "sol", "", "", "", time.Time{}, uuid.Nil, 50)
	s.Require().NoError(err)
	s.Empty(otherChain)
	s.Zero(total)
}

// Another account's rows and rows of wallets without an account never show up.
func (s *TransactionRepositoryTestSuite) TestList_ChangedForAccount_StaysInsideTheAccount() {
	fixture := s.seedChanges()

	rows, total, err := s.repo.ListChangedForAccount(context.Background(), fixture.accountID, "", "", "", "alice", time.Time{}, uuid.Nil, 50)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{fixture.ordered[0].ID, fixture.ordered[3].ID}, ids(rows))
	s.Equal(int64(2), total)
	for _, row := range rows {
		s.Equal(fixture.walletID, row.WalletID)
	}

	stranger, total, err := s.repo.ListChangedForAccount(context.Background(), uuid.New(), "", "", "", "", time.Time{}, uuid.Nil, 50)
	s.Require().NoError(err)
	s.Empty(stranger)
	s.Zero(total)
}

func (s *TransactionRepositoryTestSuite) TestList_ChangedForAccount_RequiresALimit() {
	_, _, err := s.repo.ListChangedForAccount(context.Background(), uuid.New(), "", "", "", "", time.Time{}, uuid.Nil, 0)
	s.Error(err)
}

// A row recorded before its send keeps what the send answered: the node's hash and
// the raw payload (an unknown outcome).
func (s *TransactionRepositoryTestSuite) TestRecordBroadcast_StoresTheHashAndThePayload() {
	ctx := context.Background()
	tx := s.makeTx(s.insertWallet(), models.TxTypeWithdrawal, "confirming")
	s.Require().NoError(s.repo.Create(ctx, tx))

	s.Require().NoError(s.repo.RecordBroadcast(ctx, tx.ID, "0xnodehash", `{"broadcast_outcome": "unknown", "sender_nonce": 41}`))
	found, err := s.repo.FindByID(ctx, tx.ID)
	s.Require().NoError(err)
	s.Equal("0xnodehash", found.TxHash)
	s.JSONEq(`{"broadcast_outcome":"unknown","sender_nonce":41}`, found.RawPayload)
	s.Error(s.repo.RecordBroadcast(ctx, tx.ID, " ", "{}"), "a broadcast is recorded under a hash")
}
