package walletview_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/walletview"
)

type fakeTransactionStore struct {
	walletrecords.TransactionStore
	page    []models.Transaction
	total   int64
	pageErr error
	found   *models.Transaction
	foundBy error

	gotType, gotStatus string
	gotLimit, gotSkip  int
}

func (f *fakeTransactionStore) FindByWallet(_ context.Context, _ uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error) {
	f.gotType, f.gotStatus, f.gotLimit, f.gotSkip = txType, status, limit, offset
	return f.page, f.total, f.pageErr
}

func (f *fakeTransactionStore) FindByIDAndWallet(context.Context, string, uuid.UUID) (*models.Transaction, error) {
	return f.found, f.foundBy
}

func TestTransactionsOf(t *testing.T) {
	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainETH}

	t.Run("returns the page with the chain and tokens the decimals come from", func(t *testing.T) {
		f := newFixture()
		f.txs.page, f.txs.total = []models.Transaction{{TxHash: "0x1"}}, 7
		f.tokens.rows = []models.Token{{Symbol: "USDC"}}

		page, err := f.service().TransactionsOf(context.Background(), walletview.TransactionsInput{Wallet: wallet, Type: "deposit", Status: "confirmed", Limit: 50, Offset: 100})

		require.NoError(t, err)
		assert.Equal(t, f.txs.page, page.Transactions)
		assert.Equal(t, int64(7), page.Total)
		assert.Equal(t, models.ChainETH, page.Chain.ID)
		assert.Equal(t, f.tokens.rows, page.Tokens)
		assert.Equal(t, [4]any{"deposit", "confirmed", 50, 100}, [4]any{f.txs.gotType, f.txs.gotStatus, f.txs.gotLimit, f.txs.gotSkip})
	})

	t.Run("a chain or tokens that cannot be read leave the decimals unknown, not the page failed", func(t *testing.T) {
		f := newFixture()
		f.txs.page = []models.Transaction{{TxHash: "0x1"}}
		f.catalog.err = errors.New("pq: down")
		f.tokens.err = errors.New("pq: down")

		page, err := f.service().TransactionsOf(context.Background(), walletview.TransactionsInput{Wallet: wallet})

		require.NoError(t, err)
		assert.Len(t, page.Transactions, 1)
		assert.Nil(t, page.Chain)
		assert.Nil(t, page.Tokens)
	})

	t.Run("a failed page read is marked", func(t *testing.T) {
		f := newFixture()
		f.txs.pageErr = errors.New("pq: down")

		_, err := f.service().TransactionsOf(context.Background(), walletview.TransactionsInput{Wallet: wallet})

		var failed *walletview.FetchError
		require.ErrorAs(t, err, &failed)
		assert.Equal(t, "transactions", failed.What)
	})
}

func TestTransaction(t *testing.T) {
	wallet := &models.Wallet{ID: uuid.New(), Chain: models.ChainETH}

	t.Run("returns the transaction the wallet holds", func(t *testing.T) {
		f := newFixture()
		f.txs.found = &models.Transaction{TxHash: "0x1"}

		page, err := f.service().Transaction(context.Background(), wallet, "tx")

		require.NoError(t, err)
		require.Len(t, page.Transactions, 1)
		assert.Equal(t, "0x1", page.Transactions[0].TxHash)
		assert.Equal(t, models.ChainETH, page.Chain.ID)
	})

	t.Run("a transaction the wallet does not hold, or a failed lookup, is not found", func(t *testing.T) {
		for name, store := range map[string]*fakeTransactionStore{
			"no row": {}, "outage": {foundBy: errors.New("pq: down")},
		} {
			f := newFixture()
			f.txs = store

			_, err := f.service().Transaction(context.Background(), wallet, "tx")

			assert.ErrorIs(t, err, walletview.ErrTransactionNotFound, name)
		}
	})
}
