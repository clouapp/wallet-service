package migrations_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/database/migrations"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const (
	seedHostFixture     = "smtp.seed.example"
	seedEditedHost      = "operator-edited-host"
	seedPasswordFixture = "seed-fixture-password"
)

func TestSeedPlatformSettingsSkipsABlankEnv(t *testing.T) {
	clearPlatformSeedEnv(t)
	t.Setenv("MAIL_HOST", "   ")
	t.Setenv("MAIL_PASSWORD", "  ")
	fixtures.TestDB(t)

	if err := (&migrations.M00000000000590SeedPlatformSettingsFromEnv{}).Up(); err != nil {
		t.Fatal("blank platform settings seed failed")
	}
	if count := platformSettingCount(t, "mail_smtp", "host"); count != 0 {
		t.Fatalf("blank host inserted %d rows", count)
	}
	if count := platformSettingCount(t, "mail_smtp", "password"); count != 0 {
		t.Fatalf("blank password inserted %d rows", count)
	}
	if count := scalar[int64](t, `SELECT count(*) FROM features`); count != 0 {
		t.Fatalf("features rows = %d", count)
	}
	if count := scalar[int64](t, `SELECT count(*) FROM global_features`); count != 0 {
		t.Fatalf("global feature rows = %d", count)
	}
	if count := platformSettingCount(t, "deposit_scan", "batch_blocks"); count != 0 {
		t.Fatalf("deposit scan rows = %d", count)
	}
}

func TestSeedPlatformSettingsInsertsANonSecretOnce(t *testing.T) {
	clearPlatformSeedEnv(t)
	fixtures.TestDB(t)
	t.Setenv("MAIL_HOST", seedHostFixture)
	migration := &migrations.M00000000000590SeedPlatformSettingsFromEnv{}

	if err := migration.Up(); err != nil {
		t.Fatal("platform settings seed failed")
	}
	if err := migration.Up(); err != nil {
		t.Fatal("second platform settings seed failed")
	}
	if got := platformSettingValue(t, "mail_smtp", "host"); got != seedHostFixture {
		t.Fatal("stored host did not match the fixture")
	}
	if count := platformSettingCount(t, "mail_smtp", "host"); count != 1 {
		t.Fatalf("host rows = %d", count)
	}
	if count := platformSettingCount(t, "mail_smtp", "port"); count != 0 {
		t.Fatalf("unset port rows = %d", count)
	}
}

func TestSeedPlatformSettingsSealsASecretAndOmitsItFromLogs(t *testing.T) {
	clearPlatformSeedEnv(t)
	fixtures.TestDB(t)

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	t.Setenv("MAIL_PASSWORD", seedPasswordFixture)
	if err := (&migrations.M00000000000590SeedPlatformSettingsFromEnv{}).Up(); err != nil {
		if strings.Contains(err.Error(), seedPasswordFixture) {
			t.Fatal("seed failed and the error included a secret")
		}
		t.Fatal("seed password failed")
	}

	stored := platformSettingValue(t, "mail_smtp", "password")
	if !strings.HasPrefix(stored, "enc:v1:") || strings.Contains(stored, seedPasswordFixture) {
		t.Fatal("stored password is not sealed")
	}
	opened, err := settings.CryptSealer{}.Open(stored)
	if err != nil || opened != seedPasswordFixture {
		t.Fatal("opened password did not match the fixture")
	}
	text := logs.String()
	if strings.Contains(text, seedPasswordFixture) || strings.Contains(text, stored) || strings.Contains(text, "enc:v1:") {
		t.Fatal("seed output included a secret or a sealed blob")
	}
}

func TestSeedPlatformSettingsLeavesAnEditedRow(t *testing.T) {
	clearPlatformSeedEnv(t)
	fixtures.TestDB(t)
	t.Setenv("MAIL_HOST", seedHostFixture)
	migration := &migrations.M00000000000590SeedPlatformSettingsFromEnv{}
	if err := migration.Up(); err != nil {
		t.Fatal("platform settings seed failed")
	}

	exec(t, `UPDATE settings SET value = ? WHERE account_id IS NULL AND "group" = 'mail_smtp' AND "key" = 'host'`, seedEditedHost)
	t.Setenv("MAIL_HOST", "replacement-host.example")
	if err := migration.Up(); err != nil {
		t.Fatal("second platform settings seed failed")
	}
	if err := migration.Down(); err != nil {
		t.Fatal("platform settings seed down failed")
	}
	if got := platformSettingValue(t, "mail_smtp", "host"); got != seedEditedHost {
		t.Fatal("second migrate changed an edited row")
	}
	if count := platformSettingCount(t, "mail_smtp", "host"); count != 1 {
		t.Fatalf("host rows = %d", count)
	}
}

func clearPlatformSeedEnv(t *testing.T) {
	t.Helper()
	for _, name := range settings.PlatformSettingsSeedEnvNames() {
		t.Setenv(name, "")
	}
}

func platformSettingCount(t *testing.T, group, key string) int64 {
	t.Helper()
	return scalar[int64](t, `SELECT count(*) FROM settings WHERE account_id IS NULL AND "group" = ? AND "key" = ?`, group, key)
}

func platformSettingValue(t *testing.T, group, key string) string {
	t.Helper()
	return scalar[string](t, `SELECT value FROM settings WHERE account_id IS NULL AND "group" = ? AND "key" = ?`, group, key)
}
