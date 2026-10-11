package withdraw

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// countingDecimals answers decimals for the assets it knows and counts the lookups.
type countingDecimals struct {
	known   map[string]int
	lookups int
}

func (d *countingDecimals) Decimals(chainID, asset string) (int, bool) {
	d.lookups++
	decimals, ok := d.known[chainID+"/"+asset]
	return decimals, ok
}

func newAccountTransactionsService(decimals AssetDecimals) (*Service, *memTransactions) {
	txs := newMemTransactions()
	return NewService(Deps{Transactions: txs, Decimals: decimals}), txs
}

func stampedTransaction(walletID uuid.UUID, id string, at time.Time, asset string) *models.Transaction {
	tx := &models.Transaction{
		ID: uuid.MustParse(id), WalletID: walletID, Chain: "eth", TxType: models.TxTypeDeposit,
		Status: "confirmed", Asset: asset, Amount: "1500000",
	}
	tx.UpdatedAt = carbon.NewDateTime(carbon.FromStdTime(at))
	return tx
}

func TestList_TransactionChanges_StartsAtTheLaterOfUpdatedSinceAndAfter(t *testing.T) {
	since := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	id := uuid.MustParse("00000000-0000-4000-8000-000000000007")
	cases := map[string]struct {
		since time.Time
		after TransactionPosition
		want  TransactionPosition
	}{
		"neither reads from the start":     {want: TransactionPosition{}},
		"updated_since alone is inclusive": {since: since, want: TransactionPosition{UpdatedAt: since}},
		"a cursor alone":                   {after: TransactionPosition{UpdatedAt: since, ID: id}, want: TransactionPosition{UpdatedAt: since, ID: id}},
		"a cursor past updated_since": {
			since: since.Add(-time.Hour), after: TransactionPosition{UpdatedAt: since, ID: id},
			want: TransactionPosition{UpdatedAt: since, ID: id},
		},
		"a cursor inside the updated_since second": {
			since: since, after: TransactionPosition{UpdatedAt: since, ID: id},
			want: TransactionPosition{UpdatedAt: since, ID: id},
		},
		"a cursor before updated_since": {
			since: since, after: TransactionPosition{UpdatedAt: since.Add(-time.Hour), ID: id},
			want: TransactionPosition{UpdatedAt: since},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc, txs := newAccountTransactionsService(nil)
			account := uuid.New()

			_, err := svc.ListTransactionChanges(context.Background(), TransactionChangesInput{
				AccountID: account, Chain: "eth", TxType: models.TxTypeDeposit, Status: "confirmed", ExternalUserID: "alice",
				UpdatedSince: tc.since, After: tc.after, Limit: 2,
			})
			if err != nil {
				t.Fatalf("ListTransactionChanges() error = %v", err)
			}
			call := txs.lastChangesCall
			if !call.after.UpdatedAt.Equal(tc.want.UpdatedAt) || call.after.ID != tc.want.ID {
				t.Fatalf("store read after %+v, want %+v", call.after, tc.want)
			}
			if call.accountID != account || call.chainID != "eth" || call.txType != models.TxTypeDeposit ||
				call.status != "confirmed" || call.userID != "alice" {
				t.Fatalf("store filters = %+v", call)
			}
			if call.limit != 3 {
				t.Fatalf("store limit = %d, want limit + 1 to learn whether a next page exists", call.limit)
			}
		})
	}
}

func TestList_TransactionChanges_PointsNextAtTheLastRowOnlyWhenMoreFollow(t *testing.T) {
	account, walletID := uuid.New(), uuid.New()
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	first := stampedTransaction(walletID, "00000000-0000-4000-8000-000000000001", at, "eth")
	second := stampedTransaction(walletID, "00000000-0000-4000-8000-000000000002", at, "eth")
	third := stampedTransaction(walletID, "00000000-0000-4000-8000-000000000003", at.Add(time.Second), "eth")

	svc, txs := newAccountTransactionsService(nil)
	txs.ownWallet(walletID, account)
	txs.add(third)
	txs.add(first)
	txs.add(second)

	page, err := svc.ListTransactionChanges(context.Background(), TransactionChangesInput{AccountID: account, Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(page.Transactions) != 2 || page.Transactions[0].Transaction.ID != first.ID || page.Transactions[1].Transaction.ID != second.ID {
		t.Fatalf("first page = %+v", page.Transactions)
	}
	if page.Total != 3 {
		t.Fatalf("first page total = %d, want 3", page.Total)
	}
	if page.Next == nil || !page.Next.UpdatedAt.Equal(at) || page.Next.ID != second.ID {
		t.Fatalf("first page next = %+v, want the position of the second row", page.Next)
	}

	page, err = svc.ListTransactionChanges(context.Background(), TransactionChangesInput{AccountID: account, After: *page.Next, Limit: 2})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(page.Transactions) != 1 || page.Transactions[0].Transaction.ID != third.ID || page.Total != 1 {
		t.Fatalf("second page = %+v (total %d)", page.Transactions, page.Total)
	}
	if page.Next != nil {
		t.Fatalf("last page next = %+v, want nil", page.Next)
	}

	page, err = svc.ListTransactionChanges(context.Background(), TransactionChangesInput{AccountID: account, Limit: 3})
	if err != nil {
		t.Fatalf("exact page: %v", err)
	}
	if len(page.Transactions) != 3 || page.Next != nil {
		t.Fatalf("a page that holds every row has no next: %d rows, next %+v", len(page.Transactions), page.Next)
	}
}

func TestList_TransactionChanges_EmptyFeedIsAnEmptyPage(t *testing.T) {
	svc, _ := newAccountTransactionsService(nil)

	page, err := svc.ListTransactionChanges(context.Background(), TransactionChangesInput{AccountID: uuid.New(), Limit: 50})
	if err != nil {
		t.Fatalf("ListTransactionChanges() error = %v", err)
	}
	if page.Transactions == nil || len(page.Transactions) != 0 || page.Total != 0 || page.Next != nil {
		t.Fatalf("page = %+v, want an empty, non-nil page without next", page)
	}
}

func TestList_TransactionChanges_RefusesAMissingLimitAndWrapsAStoreFailure(t *testing.T) {
	svc, txs := newAccountTransactionsService(nil)
	if _, err := svc.ListTransactionChanges(context.Background(), TransactionChangesInput{AccountID: uuid.New()}); err == nil {
		t.Fatal("a missing limit must be refused")
	}

	boom := errors.New("connection reset")
	txs.failWith(boom)
	if _, err := svc.ListTransactionChanges(context.Background(), TransactionChangesInput{AccountID: uuid.New(), Limit: 1}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the store error", err)
	}
}

func TestAccount_Transactions_CarryTheAssetDecimalsOncePerAsset(t *testing.T) {
	account, walletID := uuid.New(), uuid.New()
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	decimals := &countingDecimals{known: map[string]int{"eth/usdt": 6}}
	svc, txs := newAccountTransactionsService(decimals)
	txs.ownWallet(walletID, account)
	txs.add(stampedTransaction(walletID, "00000000-0000-4000-8000-000000000001", at, "usdt"))
	txs.add(stampedTransaction(walletID, "00000000-0000-4000-8000-000000000002", at, "usdt"))
	txs.add(stampedTransaction(walletID, "00000000-0000-4000-8000-000000000003", at, "unknown"))

	page, err := svc.ListTransactionChanges(context.Background(), TransactionChangesInput{AccountID: account, Limit: 10})
	if err != nil {
		t.Fatalf("ListTransactionChanges() error = %v", err)
	}
	if len(page.Transactions) != 3 {
		t.Fatalf("rows = %d, want 3", len(page.Transactions))
	}
	for _, row := range page.Transactions[:2] {
		if row.Decimals == nil || *row.Decimals != 6 {
			t.Fatalf("usdt decimals = %v, want 6", row.Decimals)
		}
	}
	if page.Transactions[2].Decimals != nil {
		t.Fatalf("unknown asset decimals = %v, want nil", *page.Transactions[2].Decimals)
	}
	if decimals.lookups != 2 {
		t.Fatalf("decimal lookups = %d, want one per (chain, asset)", decimals.lookups)
	}

	listed, total, err := svc.ListTransactionsForAccount(context.Background(), account, "", "", "", "", 50, 0)
	if err != nil || total != 3 || len(listed) != 3 || listed[0].Decimals == nil || *listed[0].Decimals != 6 {
		t.Fatalf("offset list = %+v, total %d, err %v", listed, total, err)
	}

	one, err := svc.GetTransactionForAccount(context.Background(), account, uuid.MustParse("00000000-0000-4000-8000-000000000001"))
	if err != nil || one.Decimals == nil || *one.Decimals != 6 {
		t.Fatalf("single transaction = %+v, err %v", one, err)
	}
}

func TestAccount_Transactions_WithoutADecimalsLookupCarryNone(t *testing.T) {
	account, walletID := uuid.New(), uuid.New()
	svc, txs := newAccountTransactionsService(nil)
	txs.ownWallet(walletID, account)
	txs.add(stampedTransaction(walletID, "00000000-0000-4000-8000-000000000001", time.Now(), "eth"))

	listed, _, err := svc.ListTransactionsForAccount(context.Background(), account, "", "", "", "", 50, 0)
	if err != nil || len(listed) != 1 || listed[0].Decimals != nil {
		t.Fatalf("offset list = %+v, err %v", listed, err)
	}
}
