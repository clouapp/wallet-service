package migrations_test

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestMain(m *testing.M) {
	testutil.BootTest()
	os.Exit(m.Run())
}

const (
	transactionAmountConstraint = "transactions_amount_non_negative"
	withdrawalAmountConstraint  = "withdrawals_amount_non_negative"
)

func exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	_, err := facades.Orm().Query().Exec(statement, args...)
	require.NoError(t, err, statement)
}

func scalar[T any](t *testing.T, query string, args ...any) T {
	t.Helper()
	var value T
	require.NoError(t, facades.Orm().Query().Raw(query, args...).Scan(&value), query)
	return value
}

func constraintCount(t *testing.T, name string) int64 {
	return scalar[int64](t, `SELECT count(*) FROM pg_constraint WHERE conname = ?`, name)
}

// legacySignedRows drops the constraints and stores what the old convention wrote:
// an outgoing transaction with a negative amount and no direction, and a negative
// withdrawal amount.
func legacySignedRows(t *testing.T) (txID, withdrawalID uuid.UUID) {
	t.Helper()
	require.NoError(t, (&migrations.M00000000000280EnforceNonNegativeAmounts{}).Down())
	wallet := fixtures.InsertWallet(t, "sol")
	txID, withdrawalID = uuid.New(), uuid.New()
	exec(t, `INSERT INTO transactions (id, wallet_id, external_user_id, chain, tx_type, tx_hash, to_address, amount, fee, asset, status, required_confs)
	         VALUES (?, ?, 'user1', 'sol', 'withdrawal', ?, 'So1Dest', '-20000000', '-5000', 'sol', 'confirmed', 1)`,
		txID, wallet.ID, uuid.NewString())
	exec(t, `INSERT INTO withdrawals (id, wallet_id, status, amount, destination_address) VALUES (?, ?, 'confirmed', -0.02, 'So1Dest')`,
		withdrawalID, wallet.ID)
	return txID, withdrawalID
}

func TestEnforce_Non_NegativeAmountsNormalizesBacksUpAndConstrains(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000280EnforceNonNegativeAmounts{}
	require.Equal(t, int64(1), constraintCount(t, transactionAmountConstraint), "migrate:fresh applies the constraint")

	txID, withdrawalID := legacySignedRows(t)
	require.Zero(t, constraintCount(t, transactionAmountConstraint))

	require.NoError(t, migration.Up())

	var tx models.Transaction
	require.NoError(t, facades.Orm().Query().Where("id = ?", txID).First(&tx))
	require.Equal(t, "20000000", tx.Amount)
	require.Equal(t, "5000", tx.Fee)
	require.Equal(t, models.TxTypeWithdrawal, tx.TxType, "the type is untouched")
	require.Equal(t, models.TxDirectionOutbound, tx.Direction, "the dropped sign becomes an explicit direction")
	require.Equal(t, "0.02", scalar[string](t, `SELECT amount::float8::text FROM withdrawals WHERE id = ?`, withdrawalID))

	backups := `SELECT count(*) FROM amount_sign_backups`
	require.Equal(t, int64(3), scalar[int64](t, backups), "transactions.amount, transactions.fee and withdrawals.amount")
	require.Equal(t, "-20000000", scalar[string](t,
		`SELECT original_value FROM amount_sign_backups WHERE table_name = 'transactions' AND column_name = 'amount' AND row_id = ?`, txID))
	require.Equal(t, "-20000000", scalar[string](t,
		`SELECT row_snapshot->>'amount' FROM amount_sign_backups WHERE table_name = 'transactions' AND column_name = 'amount' AND row_id = ?`, txID))
	require.Equal(t, int64(1), constraintCount(t, transactionAmountConstraint))
	require.Equal(t, int64(1), constraintCount(t, withdrawalAmountConstraint))

	require.NoError(t, migration.Up(), "re-running is a no-op")
	require.Equal(t, int64(3), scalar[int64](t, backups))
	require.Equal(t, int64(1), constraintCount(t, transactionAmountConstraint))

	require.NoError(t, migration.Down())
	require.Zero(t, constraintCount(t, transactionAmountConstraint))
	require.Zero(t, constraintCount(t, withdrawalAmountConstraint))
	require.Equal(t, int64(3), scalar[int64](t, backups), "Down keeps a backup that holds rows")
	require.Equal(t, "20000000", scalar[string](t, `SELECT amount FROM transactions WHERE id = ?`, txID), "Down never restores a sign")
}

func TestEnforce_Non_NegativeAmountsDownDropsAnEmptyBackup(t *testing.T) {
	fixtures.TestDB(t)
	migration := &migrations.M00000000000280EnforceNonNegativeAmounts{}

	require.NoError(t, migration.Down())
	require.False(t, scalar[bool](t, `SELECT to_regclass('amount_sign_backups') IS NOT NULL`))
	require.NoError(t, migration.Down(), "Down without the backup table")

	require.NoError(t, migration.Up())
	require.Zero(t, scalar[int64](t, `SELECT count(*) FROM amount_sign_backups`))
}
