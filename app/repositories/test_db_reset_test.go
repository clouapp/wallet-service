package repositories_test

import (
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/mocks"
)

const resetSequence = "amount_sign_backups_id_seq"

func rowCount(t *testing.T, table string) int64 {
	t.Helper()
	var count int64
	if err := facades.Orm().Query().Raw("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func nextSequenceValue(t *testing.T) int64 {
	t.Helper()
	var value int64
	if err := facades.Orm().Query().Raw("SELECT nextval(?)", resetSequence).Scan(&value); err != nil {
		t.Fatalf("nextval %s: %v", resetSequence, err)
	}
	return value
}

// TestTestDBEmptiesTablesAndSequencesBetweenTests guards the per-test reset that
// replaced migrate:fresh: rows linked by foreign keys and owned sequences are gone
// after a test, and the schema with its migrations stays.
func TestTestDBEmptiesTablesAndSequencesBetweenTests(t *testing.T) {
	var migrations int64
	t.Run("writes", func(t *testing.T) {
		mocks.TestDB(t)
		migrations = rowCount(t, "migrations")
		account := mocks.InsertAccount(t, "reset")
		mocks.InsertWalletWithAccount(t, models.ChainSOL, &account.ID)
		mocks.InsertUser(t)
		nextSequenceValue(t)
		nextSequenceValue(t)
	})

	mocks.TestDB(t)
	for _, table := range []string{"accounts", "wallets", "addresses", "users"} {
		if got := rowCount(t, table); got != 0 {
			t.Fatalf("%s has %d rows after the reset", table, got)
		}
	}
	if got := nextSequenceValue(t); got != 1 {
		t.Fatalf("%s restarted at %d, want 1", resetSequence, got)
	}
	if got := rowCount(t, "migrations"); got == 0 || got != migrations {
		t.Fatalf("migrations rows = %d, want the %d applied ones kept", got, migrations)
	}
}
