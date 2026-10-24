package fixtures_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/adapters/redis/scanactivity"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestMain(m *testing.M) {
	testutil.BootTest()
	os.Exit(m.Run())
}

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
		fixtures.TestDB(t)
		migrations = rowCount(t, "migrations")
		account := fixtures.InsertAccount(t, "reset")
		fixtures.InsertWalletWithAccount(t, models.ChainSOL, &account.ID)
		fixtures.InsertUser(t)
		nextSequenceValue(t)
		nextSequenceValue(t)
	})

	fixtures.TestDB(t)
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

// TestTestDB_ForgetsTheScanActivityARunLeft guards GET /health in the contract
// snapshot: a scan cycle or a provider push one test recorded in the worker's Redis
// index outlives the truncated rows and would add a chain to the next test's health.
func TestTestDB_ForgetsTheScanActivityARunLeft(t *testing.T) {
	ctx := context.Background()
	store := scanactivity.New(testenv.TestRedis(t))
	t.Run("writes", func(t *testing.T) {
		fixtures.TestDB(t)
		if err := store.RecordScan(ctx, "eth", deposit.ScanRecord{Head: 10, Checkpoint: 9, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordPush(ctx, "sol", time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordSignatureSync(ctx, "tsol", deposit.SignatureSyncRecord{Accounts: 36, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordMode(ctx, "btc", deposit.ScanModeRecord{Mode: deposit.ScanModeBlocks, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RaiseAlert(ctx, "scan-lag:eth", time.Hour); err != nil {
			t.Fatal(err)
		}
	})

	fixtures.TestDB(t)
	activity, err := store.ReadActivity(ctx, []string{"eth", "sol", "tsol", "btc"})
	if err != nil || len(activity) != 0 {
		t.Fatalf("activity after the reset %+v (err %v), want none", activity, err)
	}
	if raised, err := store.ClearAlert(ctx, "scan-lag:eth"); err != nil || raised {
		t.Fatalf("an alert raised before the reset is still raised (err %v)", err)
	}
}
