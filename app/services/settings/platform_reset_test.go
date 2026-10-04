package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	activitylog "github.com/macrowallets/waas/app/services/activity"
)

const platformResetSecret = "platform-section-reset-secret"

func TestResetPlatformSectionDeletesRowsForgetsCacheAndRecordsFieldNames(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	accountID := uuid.New()
	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{
		keyMailHost:     "smtp.example.test",
		keyMailPassword: "enc:v1:" + platformResetSecret,
	})
	store.PutPlatform(groupDepositScan, map[string]string{keyBatchBlocks: "80"})
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
		sealed, err := prefixSealer{}.Seal(`{"host":"stale-smtp-host","password":"` + platformResetSecret + `"}`)
		if err != nil {
			t.Fatalf("seal cache: %v", err)
		}
		cache.values[key] = sealed
	}
	if len(pageKeys) == 0 {
		t.Fatal("mail page has no platform groups")
	}

	service := NewService(store, prefixSealer{}, cache, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	view, err := service.ResetPlatformSection(context.Background(), actor, "  "+sectionMail+"  ")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if view.Name != sectionMail {
		t.Fatalf("section = %q", view.Name)
	}
	encodedView, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("encode view: %v", err)
	}
	if strings.Contains(string(encodedView), platformResetSecret) || strings.Contains(string(encodedView), "stale-smtp-host") || strings.Contains(string(encodedView), "smtp.example.test") {
		t.Fatal("the reset answer included a stored value")
	}

	next, err := service.PlatformGroup(context.Background(), actor, groupMailSMTP)
	if err != nil {
		t.Fatalf("next read: %v", err)
	}
	port := fieldByKey(t, next, keyMailPort)
	if port.Value != defaultMailPort || port.IsSet {
		t.Fatalf("port after reset = %#v set %v", port.Value, port.IsSet)
	}
	host := fieldByKey(t, next, keyMailHost)
	if host.Value != "" || host.IsSet {
		t.Fatalf("host after reset = %#v set %v", host.Value, host.IsSet)
	}
	password := fieldByKey(t, next, keyMailPassword)
	if password.IsSet || password.Value != nil {
		t.Fatal("password was still set after reset")
	}

	if _, ok := store.rows[platformStoreKey(groupMailSMTP)]; ok {
		t.Fatal("platform mail rows were kept")
	}
	if got := store.rows[platformStoreKey(groupDepositScan)][keyBatchBlocks]; got != "80" {
		t.Fatalf("deposit_scan = %q", got)
	}
	idle, ok := store.get(accountID, groupAccountSecurity, keySessionIdleMinutes)
	if !ok || idle != "45" {
		t.Fatalf("account idle = %q present %v", idle, ok)
	}

	if len(activity.rows) != len(pageKeys) {
		t.Fatalf("activity rows = %d, want %d", len(activity.rows), len(pageKeys))
	}
	seen := map[string]bool{}
	for _, row := range activity.rows {
		if row.AccountID != nil {
			t.Fatal("platform reset stored an account id")
		}
		if row.Action != activitylog.ActionSettingsSectionReset || row.TargetType != activitylog.TargetSettings || row.TargetID != sectionMail {
			t.Fatalf("activity row = %+v", row)
		}
		meta, err := json.Marshal(row.Metadata)
		if err != nil {
			t.Fatalf("metadata: %v", err)
		}
		if strings.Contains(string(meta), platformResetSecret) || strings.Contains(string(meta), "smtp.example.test") || strings.Contains(string(meta), "stale-smtp-host") || strings.Contains(string(meta), "enc:v1:") {
			t.Fatalf("metadata held a value: %s", meta)
		}
		groupName, _ := row.Metadata["group"].(string)
		fields, _ := row.Metadata["fields"].([]string)
		if groupName == "" || len(fields) == 0 || len(row.Metadata) != 2 {
			t.Fatalf("metadata = %#v", row.Metadata)
		}
		seen[groupName] = true
		if groupName == groupMailSMTP {
			if !containsAll(fields, keyMailHost, keyMailPassword, keyMailPort) {
				t.Fatalf("mail fields = %v", fields)
			}
		}
	}
	if len(seen) != len(pageKeys) {
		t.Fatalf("activity groups = %v", seen)
	}

	if len(cache.keys) != len(pageKeys) {
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
	cached, err := service.platformValues(context.Background(), groupMailSMTP)
	if err != nil {
		t.Fatalf("cached read: %v", err)
	}
	if cached[keyMailHost] != "" || strings.Contains(strings.Join(valuesOf(cached), " "), platformResetSecret) {
		t.Fatalf("cached read kept a stored value: %#v", cached)
	}
}

func TestResetPlatformSectionNotFoundComesBeforeForbidden(t *testing.T) {
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
	admins := &countingPlatformAdmins{}
	activity := &recordingActivity{}
	cache := &memoryCache{values: map[string]string{
		platformCacheKey(groupMailSMTP):           "stale-mail",
		cacheKey(accountID, groupAccountSecurity): "stale-account",
	}}
	service := NewService(store, prefixSealer{}, cache, activity).WithPlatformAdmins(admins)

	for _, section := range []string{"not-a-section", sectionSecurity, sectionLimits, "  "} {
		if _, err := service.ResetPlatformSection(context.Background(), actor, section); !errors.Is(err, ErrSectionNotFound) {
			t.Fatalf("%q = %v, want not found", section, err)
		}
	}
	if admins.calls != 0 {
		t.Fatalf("unknown section asked the admin gate %d times", admins.calls)
	}
	if len(cache.keys) != 0 || len(activity.rows) != 0 {
		t.Fatal("an unknown section changed cache or activity")
	}
	if cache.values[platformCacheKey(groupMailSMTP)] != "stale-mail" {
		t.Fatal("unknown section dropped a platform key")
	}
	if got := store.rows[platformStoreKey(groupMailSMTP)][keyMailHost]; got != "smtp.example.test" {
		t.Fatalf("unknown section deleted the platform row %q", got)
	}

	_, err := service.ResetPlatformSection(context.Background(), actor, sectionMail)
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin on a known section = %v, want forbidden", err)
	}
	if admins.calls != 1 {
		t.Fatalf("admin checks = %d, want 1", admins.calls)
	}
	if len(cache.keys) != 0 || len(activity.rows) != 0 {
		t.Fatal("a refused reset changed cache or activity")
	}
	if got := store.rows[platformStoreKey(groupMailSMTP)][keyMailHost]; got != "smtp.example.test" {
		t.Fatalf("refused reset deleted the platform row %q", got)
	}
	idle, ok := store.get(accountID, groupAccountSecurity, keySessionIdleMinutes)
	if !ok || idle != "45" {
		t.Fatalf("account idle = %q present %v", idle, ok)
	}
}

func TestResetPlatformSectionRequiresActorAdminCacheAndStore(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	store := newMemoryStore()
	store.PutPlatform(groupMailSMTP, map[string]string{keyMailHost: "smtp.example.test"})
	activity := &recordingActivity{}

	if _, err := (*Service)(nil).ResetPlatformSection(context.Background(), actor, sectionMail); !errors.Is(err, errServiceRequired) {
		t.Fatalf("nil service = %v", err)
	}
	service := NewService(store, prefixSealer{}, &memoryCache{}, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	if _, err := service.ResetPlatformSection(nil, actor, sectionMail); err == nil {
		t.Fatal("nil context was accepted")
	}
	if _, err := service.ResetPlatformSection(context.Background(), uuid.Nil, "not-a-section"); !errors.Is(err, ErrSectionNotFound) {
		t.Fatalf("nil actor on an unknown section = %v, want not found", err)
	}
	if _, err := service.ResetPlatformSection(context.Background(), uuid.Nil, sectionMail); err == nil {
		t.Fatal("nil actor on a known section was accepted")
	}
	unwired := NewService(store, prefixSealer{}, &memoryCache{}, activity)
	if _, err := unwired.ResetPlatformSection(context.Background(), actor, sectionMail); err == nil {
		t.Fatal("missing platform admins was accepted")
	}
	service.activity = nil
	if _, err := service.ResetPlatformSection(context.Background(), actor, sectionMail); err == nil {
		t.Fatal("missing activity log was accepted")
	}
	service.activity = activity
	service.store = storeWithoutPlatformDelete{}
	if _, err := service.ResetPlatformSection(context.Background(), actor, sectionMail); err == nil {
		t.Fatal("a store that cannot delete a platform group was accepted")
	}
	service.store = store
	service.cache = nil
	if _, err := service.ResetPlatformSection(context.Background(), actor, sectionMail); err == nil {
		t.Fatal("missing cache was accepted")
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want 0", len(activity.rows))
	}
	if got := store.rows[platformStoreKey(groupMailSMTP)][keyMailHost]; got != "smtp.example.test" {
		t.Fatalf("a refused reset deleted the platform row %q", got)
	}
}

func TestResetPlatformSectionLeavesTheCacheWhenActivityFails(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	store := newMemoryStore()
	store.PutPlatform(groupWebhookDelivery, map[string]string{keyMaxAttempts: "4"})
	activity := &recordingActivity{err: errors.New("activity failed")}
	cache := &memoryCache{values: map[string]string{
		platformCacheKey(groupWebhookDelivery): "stale-delivery",
	}}
	service := NewService(store, prefixSealer{}, cache, activity).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})

	if _, err := service.ResetPlatformSection(context.Background(), actor, sectionDelivery); err == nil {
		t.Fatal("a failed activity write was accepted")
	}
	if len(cache.keys) != 0 {
		t.Fatalf("failed reset forgot %v", cache.keys)
	}
	if cache.values[platformCacheKey(groupWebhookDelivery)] != "stale-delivery" {
		t.Fatal("failed reset dropped the platform key")
	}
}

type storeWithoutPlatformDelete struct{}

func (storeWithoutPlatformDelete) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, nil
}

func (storeWithoutPlatformDelete) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	return nil
}

func containsAll(fields []string, want ...string) bool {
	got := map[string]bool{}
	for _, field := range fields {
		got[field] = true
	}
	for _, field := range want {
		if !got[field] {
			return false
		}
	}
	return true
}

func valuesOf(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
