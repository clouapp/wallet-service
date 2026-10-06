package repositories_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

func TestSettingsReadStoresAJSONMapForTenMinutes(t *testing.T) {
	fixtures.TestDB(t)
	ctx := context.Background()
	settingsRepo := repositories.NewSettingRepository(nil)
	activityRepo := repositories.NewAccountActivityRepository(nil)
	account := fixtures.InsertAccount(t, "settings-cache")
	actorID := uuid.New()
	if _, err := facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		actorID, actorID.String()+"@example.com", "hash", "active",
	); err != nil {
		t.Fatalf("insert actor: %v", err)
	}

	service := settings.NewService(settings.Deps{Store: settingsRepo, Sealer: settings.CryptSealer{}, Cache: settings.FacadeCache{}, Activity: activityRepo})
	if _, err := service.Save(ctx, account.ID, actorID, "owner", "account_security", map[string]any{
		"require_2fa": true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	key := "settings:account:" + account.ID.String() + ":account_security"
	t.Cleanup(func() { facades.Cache().Forget(key) })
	if facades.Cache().Has(key) {
		t.Fatal("save left the cache key in place")
	}

	enabled, err := service.Require2FA(ctx, account.ID)
	if err != nil || !enabled {
		t.Fatalf("read after write = %v, %v", enabled, err)
	}
	if !facades.Cache().Has(key) {
		t.Fatal("read after write missed the cache key")
	}
	decoded := settingsCacheJSON(t, facades.Cache().GetString(key))
	if decoded["require_2fa"] != "true" {
		t.Fatal("cache payload is not the stored JSON map")
	}
	ttl := settingsCacheTTL(t, key)
	if ttl < 9*time.Minute || ttl > 10*time.Minute {
		t.Fatalf("ttl = %s, want 10 minutes", ttl)
	}

	if err := settingsRepo.UpsertMany(ctx, account.ID, "account_security", map[string]string{
		"require_2fa": "false",
	}); err != nil {
		t.Fatalf("update row: %v", err)
	}
	cached, err := service.Require2FA(ctx, account.ID)
	if err != nil || !cached {
		t.Fatalf("cached read = %v, %v", cached, err)
	}

	if err := facades.Cache().Put(key, "not-sealed", 10*time.Minute); err != nil {
		t.Fatalf("replace cache: %v", err)
	}
	fallen, err := service.Require2FA(ctx, account.ID)
	if err != nil || fallen {
		t.Fatalf("corrupt seal = %v, %v", fallen, err)
	}
	replaced := settingsCacheJSON(t, facades.Cache().GetString(key))
	if replaced["require_2fa"] != "false" {
		t.Fatal("fallthrough did not store a JSON map")
	}

	if err := service.FlushSection(ctx, account.ID, "owner", "security"); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if facades.Cache().Has(key) {
		t.Fatal("flush left the cache key")
	}
}

func settingsCacheTTL(t *testing.T, logicalKey string) time.Duration {
	t.Helper()
	client := testutil.TestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var matched string
	iter := client.Scan(ctx, 0, "*"+logicalKey, 20).Iterator()
	for iter.Next(ctx) {
		matched = iter.Val()
		break
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("scan cache key: %v", err)
	}
	if matched == "" {
		t.Fatal("cache key is missing from redis")
	}
	ttl, err := client.TTL(ctx, matched).Result()
	if err != nil {
		t.Fatalf("read ttl: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = client.Del(cleanupCtx, matched).Err()
	})
	if ttl <= 0 {
		t.Fatal("cache key has no expiry")
	}
	return ttl
}

func settingsCacheJSON(t *testing.T, raw string) map[string]string {
	t.Helper()
	var decoded map[string]string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil || decoded == nil {
		t.Fatalf("cache payload is not a JSON map: %q", raw)
	}
	return decoded
}
