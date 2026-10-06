package testutil

import (
	"context"
	"testing"

	"github.com/macrowallets/waas/database/seeds"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// SeededTestDB extends fixtures.TestDB(t) with the `chains` and `tokens` seed
// fixtures. Use this in controller tests that create wallets or reference
// chain/token IDs — the ORM enforces FK constraints against those tables, so
// inserts fail fast if the fixtures aren't present.
//
// Skips the test when the DB is unavailable (same behaviour as fixtures.TestDB)
// unless TEST_DB_REQUIRED=1 is set, in which case unavailability is a hard
// failure. CI should export TEST_DB_REQUIRED=1 so DB-backed tests can't hide.
func SeededTestDB(t *testing.T) {
	t.Helper()
	fixtures.TestDB(t)

	ctx := context.Background()
	if err := seeds.SeedChains(ctx); err != nil {
		t.Fatalf("SeedChains: %v", err)
	}
	if err := seeds.SeedTokens(ctx); err != nil {
		t.Fatalf("SeedTokens: %v", err)
	}
}
