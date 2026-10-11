package transactions

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
	"github.com/macrowallets/waas/tests/mocks"
)

// changeRow is one transaction a changes test seeds, with the updated_at it is stamped with.
type changeRow struct {
	id     uuid.UUID
	at     time.Time
	txType string
	status string
	user   string
}

// seedChanges inserts rows on a new wallet of accountID and stamps their updated_at.
func (s *TransactionsControllerTestSuite) seedChanges(accountID uuid.UUID, rows ...changeRow) uuid.UUID {
	s.T().Helper()
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &accountID)
	for _, row := range rows {
		tx := &models.Transaction{
			ID: row.id, WalletID: wallet.ID, ExternalUserID: row.user, Chain: "eth", TxType: row.txType,
			TxHash: "0x" + uuid.NewString(), ToAddress: "0xrecipient", Amount: "1500000000000000000", Asset: "ETH",
			Status: row.status, RequiredConfs: 12, Direction: models.TxDirectionInbound, Source: models.TxSourceChain,
			RawPayload: "{}",
		}
		s.Require().NoError(facades.Orm().Query().Create(tx))
		s.stamp(row.id, row.at)
	}
	return wallet.ID
}

func (s *TransactionsControllerTestSuite) stamp(id uuid.UUID, at time.Time) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(`UPDATE transactions SET updated_at = ? WHERE id = ?`, at.UTC(), id)
	s.Require().NoError(err)
}

// changesPage is the body of one changes page; keys lists the envelope keys.
type changesPage struct {
	Data       []map[string]any `json:"data"`
	Total      int64            `json:"total"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
	NextCursor *string          `json:"next_cursor"`
	keys       []string
}

func (s *TransactionsControllerTestSuite) getPage(path, bearer string) changesPage {
	s.T().Helper()
	resp := s.External(path, ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var page changesPage
	s.Require().NoError(json.Unmarshal([]byte(content), &page))
	var envelope map[string]json.RawMessage
	s.Require().NoError(json.Unmarshal([]byte(content), &envelope))
	for key := range envelope {
		page.keys = append(page.keys, key)
	}
	sort.Strings(page.keys)
	return page
}

func idsOf(page changesPage) []string {
	out := make([]string, len(page.Data))
	for i, item := range page.Data {
		out[i], _ = item["id"].(string)
	}
	return out
}

func rowID(n int) uuid.UUID {
	return uuid.MustParse("00000000-0000-4000-8000-" + leftPad(n))
}

func leftPad(n int) string {
	digits := []byte("000000000000")
	for i := len(digits) - 1; n > 0; i-- {
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits)
}

func strs(ids ...uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func feedPath(query url.Values) string {
	return "/api/v1/transactions?" + query.Encode()
}

var changesBase = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func (s *TransactionsControllerTestSuite) TestChanges_FirstPage_StartsAtUpdatedSinceInChangeOrder() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	s.seedChanges(accountID,
		changeRow{rowID(3), changesBase.Add(2 * time.Minute), models.TxTypeDeposit, "confirmed", "alice"},
		changeRow{rowID(1), changesBase.Add(-time.Minute), models.TxTypeDeposit, "confirmed", "alice"},
		changeRow{rowID(2), changesBase, models.TxTypeDeposit, "pending", "alice"},
	)

	page := s.getPage(feedPath(url.Values{"updated_since": {"2026-10-01T09:00:00-03:00"}}), bearer)

	s.Equal(strs(rowID(2), rowID(3)), idsOf(page), "updated_since is inclusive and the order is updated_at ascending")
	s.Equal([]string{"data", "limit", "next_cursor", "offset", "total"}, page.keys)
	s.Nil(page.NextCursor)
	s.Equal(int64(2), page.Total)
	s.Equal(50, page.Limit)
	s.Zero(page.Offset)
}

func (s *TransactionsControllerTestSuite) TestChanges_FollowsNextCursorToTheEndThroughTiesOnUpdatedAt() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	// Five rows share one updated_at second; the cursor has to break the tie by id.
	var rows []changeRow
	for _, n := range []int{5, 2, 7, 1, 4} {
		rows = append(rows, changeRow{rowID(n), changesBase, models.TxTypeDeposit, "confirmed", "alice"})
	}
	rows = append(rows,
		changeRow{rowID(3), changesBase.Add(time.Second), models.TxTypeDeposit, "confirmed", "alice"},
		changeRow{rowID(6), changesBase.Add(-time.Hour), models.TxTypeDeposit, "confirmed", "alice"},
	)
	s.seedChanges(accountID, rows...)

	var walked []string
	var totals []int64
	query := url.Values{"updated_since": {"2026-10-01T12:00:00Z"}, "limit": {"2"}}
	for pages := 0; pages < 10; pages++ {
		page := s.getPage(feedPath(query), bearer)
		walked = append(walked, idsOf(page)...)
		totals = append(totals, page.Total)
		if page.NextCursor == nil {
			break
		}
		s.Len(page.Data, 2)
		query.Set("cursor", *page.NextCursor)
	}

	s.Equal(strs(rowID(1), rowID(2), rowID(4), rowID(5), rowID(7), rowID(3)), walked,
		"every row once, ties ordered by id, the row before updated_since left out")
	s.Equal([]int64{6, 4, 2}, totals, "total counts the rows from each page's start")
}

func (s *TransactionsControllerTestSuite) TestChanges_CombinesTheFiltersWithTheCursor() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	s.seedChanges(accountID,
		changeRow{rowID(1), changesBase, models.TxTypeDeposit, "confirmed", "alice"},
		changeRow{rowID(2), changesBase, models.TxTypeWithdrawal, "confirmed", "alice"},
		changeRow{rowID(3), changesBase, models.TxTypeDeposit, "pending", "alice"},
		changeRow{rowID(4), changesBase, models.TxTypeDeposit, "confirmed", "bob"},
		changeRow{rowID(5), changesBase.Add(time.Second), models.TxTypeDeposit, "confirmed", "alice"},
	)

	query := url.Values{
		"updated_since": {"2026-10-01T00:00:00Z"}, "chain": {"eth"}, "type": {"deposit"},
		"status": {"confirmed"}, "user_id": {"alice"}, "limit": {"1"},
	}
	first := s.getPage(feedPath(query), bearer)
	s.Equal(strs(rowID(1)), idsOf(first))
	s.Equal(int64(2), first.Total)
	s.Require().NotNil(first.NextCursor)

	query.Set("cursor", *first.NextCursor)
	second := s.getPage(feedPath(query), bearer)
	s.Equal(strs(rowID(5)), idsOf(second))
	s.Nil(second.NextCursor)

	otherChain := s.getPage(feedPath(url.Values{"cursor": {*first.NextCursor}, "chain": {"sol"}}), bearer)
	s.Empty(otherChain.Data)
	s.Nil(otherChain.NextCursor)
}

func (s *TransactionsControllerTestSuite) TestChanges_RefusesWhatItCannotRead() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	token := ctltestutil.Token{Bearer: bearer}

	cases := map[string]string{
		feedPath(url.Values{"cursor": {"not-a-cursor"}}):                                 "cursor is invalid",
		feedPath(url.Values{"cursor": {"eyJ1cGRhdGVkX2F0IjoiMjAyNiJ9"}}):                 "cursor is invalid",
		feedPath(url.Values{"updated_since": {"yesterday"}}):                             "updated_since must be an RFC 3339 timestamp",
		feedPath(url.Values{"updated_since": {"2026-10-01 12:00:00"}}):                   "updated_since must be an RFC 3339 timestamp",
		"/api/v1/transactions?updated_since=2026-10-01T12:00:00+00:00":                   "updated_since must be an RFC 3339 timestamp",
		feedPath(url.Values{"updated_since": {"2026-10-01T12:00:00Z"}, "offset": {"5"}}): "offset cannot be combined with updated_since or cursor",
		feedPath(url.Values{"updated_since": {"2026-10-01T12:00:00Z"}, "limit": {"0"}}):  "limit must be a positive integer",
	}
	for path, message := range cases {
		s.Run(path, func() {
			s.AssertError(s.External(path, token).Get(), 400, "invalid_request", message)
		})
	}
}

// A token reads only its account, whatever cursor it presents.
func (s *TransactionsControllerTestSuite) TestChanges_StaysInsideTheTokenAccount() {
	testutil.SeededTestDB(s.T())
	accountA, bearerA, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	accountB, bearerB, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	s.seedChanges(accountA,
		changeRow{rowID(1), changesBase, models.TxTypeDeposit, "confirmed", "shared"},
		changeRow{rowID(2), changesBase, models.TxTypeDeposit, "confirmed", "shared"},
		changeRow{rowID(3), changesBase, models.TxTypeDeposit, "confirmed", "shared"},
	)
	s.seedChanges(accountB, changeRow{rowID(9), changesBase.Add(time.Minute), models.TxTypeDeposit, "confirmed", "shared"})

	since := url.Values{"updated_since": {"2026-01-01T00:00:00Z"}, "limit": {"1"}}
	pageA := s.getPage(feedPath(since), bearerA)
	s.Equal(strs(rowID(1)), idsOf(pageA))
	s.Equal(int64(3), pageA.Total)
	s.Require().NotNil(pageA.NextCursor)

	pageB := s.getPage(feedPath(url.Values{"updated_since": {"2026-01-01T00:00:00Z"}, "user_id": {"shared"}}), bearerB)
	s.Equal(strs(rowID(9)), idsOf(pageB))

	withACursor := s.getPage(feedPath(url.Values{"cursor": {*pageA.NextCursor}}), bearerB)
	s.Equal(strs(rowID(9)), idsOf(withACursor), "another account's cursor is only a position")
}

// A confirmation moves updated_at, so a reconciler that reads again from an
// earlier updated_since sees the deposit once more, now confirmed.
func (s *TransactionsControllerTestSuite) TestChanges_ReturnAConfirmedDepositAgain() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	s.seedChanges(accountID, changeRow{rowID(1), changesBase, models.TxTypeDeposit, "pending", "alice"})
	readFrom := url.Values{"updated_since": {"2026-10-01T12:00:00Z"}}

	before := s.getPage(feedPath(readFrom), bearer)
	s.Require().Len(before.Data, 1)
	s.Equal("pending", before.Data[0]["status"])

	confirmedAt := time.Now().UTC()
	s.Require().NoError(repositories.NewTransactionRepository(nil).
		RecordConfirmations(context.Background(), rowID(1), 12, "confirmed", &confirmedAt))

	after := s.getPage(feedPath(url.Values{"updated_since": {time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)}}), bearer)
	s.Require().Len(after.Data, 1, "the confirmation moved updated_at past the earlier read")
	s.Equal("confirmed", after.Data[0]["status"])
	s.EqualValues(12, after.Data[0]["confirmations"])
}

// Without updated_since or cursor the list keeps its offset envelope and its
// newest-first order.
func (s *TransactionsControllerTestSuite) TestList_Transactions_WithoutTheFeedKeepsTheOffsetPage() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	s.seedChanges(accountID,
		changeRow{rowID(1), changesBase, models.TxTypeDeposit, "confirmed", "alice"},
		changeRow{rowID(2), changesBase.Add(time.Hour), models.TxTypeDeposit, "confirmed", "alice"},
	)
	_, err := facades.Orm().Query().Exec(`UPDATE transactions SET created_at = updated_at WHERE id IN (?, ?)`, rowID(1), rowID(2))
	s.Require().NoError(err)

	page := s.getPage("/api/v1/transactions?limit=500&offset=0", bearer)

	s.Equal([]string{"data", "limit", "offset", "total"}, page.keys)
	s.Equal(strs(rowID(2), rowID(1)), idsOf(page))
	s.Equal(50, page.Limit, "an out-of-range limit still falls back to the default")
}

// knowEth registers an eth adapter, so the asset decimals resolve the way they
// do once the chain is loaded at boot (the suite boots before the chains are
// seeded). The previous adapter, if any, comes back after the test.
func (s *TransactionsControllerTestSuite) knowEth() {
	s.T().Helper()
	registry := container.MustMake[*chainpkg.Registry]()
	previous, err := registry.Chain(models.ChainETH)
	registry.RegisterChain(mocks.NewMockChain(models.ChainETH))
	s.T().Cleanup(func() {
		if err == nil && previous != nil {
			registry.RegisterChain(previous)
		}
	})
}

// Each listed item carries the deposit webhook fields a receiver credits from.
func (s *TransactionsControllerTestSuite) TestTransactions_CarryTheDepositWebhookFields() {
	testutil.SeededTestDB(s.T())
	s.knowEth()
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	s.seedChanges(accountID, changeRow{rowID(1), changesBase, models.TxTypeDeposit, "confirmed", "alice"})

	for name, path := range map[string]string{
		"offset page":  "/api/v1/transactions",
		"changes page": feedPath(url.Values{"updated_since": {"2026-10-01T00:00:00Z"}}),
	} {
		s.Run(name, func() {
			page := s.getPage(path, bearer)
			s.Require().Len(page.Data, 1)
			item := page.Data[0]
			s.Equal(rowID(1).String(), item["transaction_id"])
			s.Equal("1500000000000000000", item["amount"])
			s.Equal("1500000000000000000", item["amount_base_units"])
			s.EqualValues(18, item["decimals"])
			s.EqualValues(12, item["required_confirmations"])
			s.Equal("alice", item["external_user_id"])
		})
	}

	s.External("/api/v1/transactions/"+rowID(1).String(), ctltestutil.Token{Bearer: bearer}).Get().
		AssertOk().
		AssertJson(map[string]any{
			"transaction_id":         rowID(1).String(),
			"amount_base_units":      "1500000000000000000",
			"decimals":               float64(18),
			"required_confirmations": float64(12),
		})
}
