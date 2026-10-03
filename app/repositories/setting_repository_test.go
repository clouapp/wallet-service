package repositories_test

import (
	"context"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

func TestListPlatformReadsOnlyPlatformRows(t *testing.T) {
	mocks.TestDB(t)
	ctx := context.Background()
	repo := repositories.NewSettingRepository(nil)
	account := mocks.InsertAccount(t, "deposit-scan-settings")

	if err := repo.UpsertMany(ctx, account.ID, "deposit_scan", map[string]string{
		"batch_blocks": "999",
	}); err != nil {
		t.Fatalf("store account row: %v", err)
	}
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO settings (account_id, "group", "key", value, created_at, updated_at)
		 VALUES (NULL, ?, ?, ?, NOW(), NOW())`,
		"deposit_scan", "batch_blocks", "80",
	); err != nil {
		t.Fatalf("store platform row: %v", err)
	}

	rows, err := repo.ListPlatform(ctx, "deposit_scan")
	if err != nil {
		t.Fatalf("list platform: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != "batch_blocks" || rows[0].Value != "80" || rows[0].AccountID != nil {
		t.Fatalf("platform rows = %+v", rows)
	}

	missing, err := repo.ListPlatform(ctx, "deposit_scan_absent")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing group = %+v, %v", missing, err)
	}
	if _, err := repo.ListPlatform(ctx, " "); err == nil {
		t.Fatal("expected a blank group to be rejected")
	}
}
