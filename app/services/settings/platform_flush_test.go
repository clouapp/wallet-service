package settings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFlushPlatformSectionForgetsThePageAndLeavesStoredRows(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	accountID := uuid.New()
	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{keyMailHost: "smtp.example.test"})
	if err := store.UpsertMany(context.Background(), accountID, groupAccountSecurity, map[string]string{
		keySessionIdleMinutes: "45",
	}); err != nil {
		t.Fatalf("store account row: %v", err)
	}
	activity := &recordingActivity{}
	cache := &memoryCache{values: map[string]string{}, ttls: map[string]time.Duration{}}
	accountKey := cacheKey(accountID, groupAccountSecurity)
	scanKey := platformCacheKey(groupDepositScan)
	cache.values[accountKey] = "stale-account"
	cache.values[scanKey] = "stale-scan"
	var pageKeys []string
	for _, group := range GroupsInSection(sectionMail) {
		if group.Scope != ScopePlatform {
			t.Fatalf("mail page group %s is %s", group.Name, group.Scope)
		}
		key := platformCacheKey(group.Name)
		pageKeys = append(pageKeys, key)
		cache.values[key] = "stale-" + group.Name
	}
	if len(pageKeys) == 0 {
		t.Fatal("mail page has no platform groups")
	}
	service := NewService(store, prefixSealer{}, cache, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	if err := service.FlushPlatformSection(context.Background(), actor, "  "+sectionMail+"  "); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want 0", len(activity.rows))
	}
	host, ok := store.rows[platformStoreKey(groupMailSMTP)][keyMailHost]
	if !ok || host != "smtp.example.test" {
		t.Fatalf("stored mail host = %q, present %v", host, ok)
	}
	idle, ok := store.get(accountID, groupAccountSecurity, keySessionIdleMinutes)
	if !ok || idle != "45" {
		t.Fatalf("stored account idle = %q, present %v", idle, ok)
	}
	if cache.keys == nil || len(cache.keys) != len(pageKeys) {
		t.Fatalf("forgotten keys = %v, want %v", cache.keys, pageKeys)
	}
	for i, key := range pageKeys {
		if cache.keys[i] != key {
			t.Fatalf("forgotten[%d] = %s, want %s", i, cache.keys[i], key)
		}
		if _, still := cache.values[key]; still {
			t.Fatalf("cache still holds %s", key)
		}
	}
	if cache.values[scanKey] != "stale-scan" {
		t.Fatalf("scan cache = %q", cache.values[scanKey])
	}
	if cache.values[accountKey] != "stale-account" {
		t.Fatalf("account cache = %q", cache.values[accountKey])
	}
}

func TestFlushPlatformSectionNotFoundComesBeforeForbidden(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	admins := &countingPlatformAdmins{}
	cache := &memoryCache{values: map[string]string{
		platformCacheKey(groupMailSMTP):            "stale-mail",
		cacheKey(uuid.New(), groupAccountSecurity): "stale-account",
	}}
	service := NewService(newMemoryStore(), prefixSealer{}, cache, &recordingActivity{}).
		WithPlatformAdmins(admins)

	for _, section := range []string{"not-a-section", sectionSecurity, sectionLimits, "  "} {
		if err := service.FlushPlatformSection(context.Background(), actor, section); !errors.Is(err, ErrSectionNotFound) {
			t.Fatalf("%q = %v, want not found", section, err)
		}
	}
	if admins.calls != 0 {
		t.Fatalf("unknown section asked the admin gate %d times", admins.calls)
	}
	if len(cache.keys) != 0 {
		t.Fatalf("unknown section forgot %v", cache.keys)
	}
	if cache.values[platformCacheKey(groupMailSMTP)] != "stale-mail" {
		t.Fatal("unknown section dropped a platform key")
	}

	err := service.FlushPlatformSection(context.Background(), actor, sectionMail)
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin on a known section = %v, want forbidden", err)
	}
	if admins.calls != 1 {
		t.Fatalf("admin checks = %d, want 1", admins.calls)
	}
	if len(cache.keys) != 0 || len(service.activity.(*recordingActivity).rows) != 0 {
		t.Fatal("a refused flush changed cache or activity")
	}
	if cache.values[platformCacheKey(groupMailSMTP)] != "stale-mail" {
		t.Fatal("a refused flush dropped the platform key")
	}
}

func TestFlushPlatformSectionRequiresActorAdminAndCache(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	store := newMemoryStore()
	activity := &recordingActivity{}

	if err := (*Service)(nil).FlushPlatformSection(context.Background(), actor, sectionMail); !errors.Is(err, errServiceRequired) {
		t.Fatalf("nil service = %v", err)
	}
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	if err := service.FlushPlatformSection(nil, actor, sectionMail); err == nil {
		t.Fatal("nil context was accepted")
	}
	if err := service.FlushPlatformSection(context.Background(), uuid.Nil, "not-a-section"); !errors.Is(err, ErrSectionNotFound) {
		t.Fatalf("nil actor on an unknown section = %v, want not found", err)
	}
	if err := service.FlushPlatformSection(context.Background(), uuid.Nil, sectionMail); err == nil {
		t.Fatal("nil actor on a known section was accepted")
	}
	unwired := NewService(store, prefixSealer{}, &memoryCache{}, activity)
	if err := unwired.FlushPlatformSection(context.Background(), actor, sectionMail); err == nil {
		t.Fatal("missing platform admins was accepted")
	}
	service.cache = nil
	if err := service.FlushPlatformSection(context.Background(), actor, sectionMail); err == nil {
		t.Fatal("missing cache was accepted")
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want 0", len(activity.rows))
	}
}
