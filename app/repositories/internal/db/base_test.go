package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func insertCurrency(tx orm.Query, code string) error {
	return tx.Create(&models.Currency{ID: uuid.New(), Name: code, Code: code, Type: models.CurrencyTypeFiat})
}

func currencyCount(t *testing.T, code string) int64 {
	t.Helper()
	count, err := facades.Orm().Query().Model(&models.Currency{}).Where("code = ?", code).Count()
	require.NoError(t, err)
	return count
}

func TestBase_Transaction_CommitsAndRollsBack(t *testing.T) {
	testutil.BootTest()
	fixtures.TestDB(t)
	ctx := context.Background()

	require.NoError(t, db.NewBase(nil).Transaction(ctx, func(tx orm.Query) error {
		return insertCurrency(tx, "TXA")
	}))
	require.EqualValues(t, 1, currencyCount(t, "TXA"))

	boom := errors.New("boom")
	err := db.NewBase(nil).Transaction(ctx, func(tx orm.Query) error {
		if err := insertCurrency(tx, "TXB"); err != nil {
			return err
		}
		return boom
	})
	require.ErrorIs(t, err, boom)
	require.EqualValues(t, 0, currencyCount(t, "TXB"))
}

func TestBase_Transaction_PanicRollsBackAndPropagates(t *testing.T) {
	testutil.BootTest()
	fixtures.TestDB(t)

	var leaked orm.Query
	require.PanicsWithValue(t, "kaboom", func() {
		_ = db.NewBase(nil).Transaction(context.Background(), func(tx orm.Query) error {
			leaked = tx
			if err := insertCurrency(tx, "TXP"); err != nil {
				return err
			}
			panic("kaboom")
		})
	})

	require.NotNil(t, leaked)
	require.Error(t, leaked.Commit(), "the transaction must already be closed")
	require.EqualValues(t, 0, currencyCount(t, "TXP"))
}

func TestBase_Transaction_JoinsTheTransactionOnTheContext(t *testing.T) {
	testutil.BootTest()
	fixtures.TestDB(t)

	outer, err := facades.Orm().Query().BeginTransaction()
	require.NoError(t, err)
	ctx := db.WithTx(context.Background(), outer)

	require.NoError(t, db.NewBase(nil).Transaction(ctx, func(tx orm.Query) error {
		return insertCurrency(tx, "TXJ")
	}))
	require.EqualValues(t, 0, currencyCount(t, "TXJ"), "the inner call must not commit the outer transaction")

	require.NoError(t, outer.Rollback())
	require.EqualValues(t, 0, currencyCount(t, "TXJ"))
}
