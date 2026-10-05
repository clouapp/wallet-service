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
)

// shiftCipher hides a payload. A sealed secret in the cached JSON map is not
// the original plaintext.
type shiftCipher struct{}

func (shiftCipher) EncryptString(value string) (string, error) {
	return shiftText(value, 1), nil
}

func (shiftCipher) DecryptString(value string) (string, error) {
	return shiftText(value, -1), nil
}

func shiftText(value string, delta rune) string {
	runes := []rune(value)
	for i, r := range runes {
		runes[i] = r + delta
	}
	return string(runes)
}

type shiftSealer struct{}

func (shiftSealer) Seal(plaintext string) (string, error) {
	return Seal(shiftCipher{}, plaintext)
}

func (shiftSealer) Open(value string) (string, error) {
	return Open(shiftCipher{}, value)
}

type countingStore struct {
	*memoryStore
	accountReads  int
	platformReads int
	accountErr    error
	platformErr   error
}

func newCountingStore() *countingStore {
	return &countingStore{memoryStore: newMemoryStore()}
}

func (s *countingStore) ListGroup(ctx context.Context, accountID uuid.UUID, group string) ([]models.Setting, error) {
	s.accountReads++
	if s.accountErr != nil {
		return nil, s.accountErr
	}
	return s.memoryStore.ListGroup(ctx, accountID, group)
}

func (s *countingStore) ListPlatform(ctx context.Context, group string) ([]models.Setting, error) {
	s.platformReads++
	if s.platformErr != nil {
		return nil, s.platformErr
	}
	return s.memoryStore.ListPlatform(ctx, group)
}

func TestSettingsCacheTTLIsTenMinutes(t *testing.T) {
	t.Parallel()

	if settingsCacheTTL != 10*time.Minute {
		t.Fatalf("ttl = %s", settingsCacheTTL)
	}
}

func TestReadAfterWriteHitsTheJSONMapCache(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-value"
	store := newCountingStore()
	cache := &memoryCache{}
	sealer := shiftSealer{}
	service := NewService(store, sealer, cache, discardActivity{})
	accountID := uuid.New()
	actorID := uuid.New()
	ctx := context.Background()

	if _, err := service.Save(ctx, accountID, actorID, "owner", groupAccountWebhooks, map[string]any{
		keySigningSecret: secret,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	webhookKey := cacheKey(accountID, groupAccountWebhooks)
	if _, ok := cache.values[webhookKey]; ok {
		t.Fatal("save left a cache key in place")
	}

	first, err := service.storedValues(ctx, accountID, groupAccountWebhooks)
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	if first[keySigningSecret] == "" {
		t.Fatal("read after write missed the stored secret")
	}
	raw, ok := cache.values[webhookKey]
	if !ok {
		t.Fatal("read after write missed the cache key")
	}
	if cache.ttls[webhookKey] != 10*time.Minute {
		t.Fatalf("ttl = %s", cache.ttls[webhookKey])
	}
	decoded := cachedJSONMap(t, raw)
	storedSecret := decoded[keySigningSecret]
	if storedSecret != first[keySigningSecret] || !IsSealed(storedSecret) || !strings.HasPrefix(storedSecret, "enc:v1:") || strings.Contains(raw, secret) {
		t.Fatal("cached secret is not ciphertext")
	}
	reads := store.accountReads
	second, err := service.storedValues(ctx, accountID, groupAccountWebhooks)
	if err != nil {
		t.Fatalf("cached read: %v", err)
	}
	if second[keySigningSecret] != first[keySigningSecret] {
		t.Fatal("cached read changed the stored secret")
	}
	if store.accountReads != reads {
		t.Fatalf("cached read hit the database %d extra times", store.accountReads-reads)
	}
}

func TestRequire2FAUsesTheCacheUntilFlushResetOrSaveForgetsIt(t *testing.T) {
	t.Parallel()

	store := newCountingStore()
	cache := &memoryCache{}
	service := NewService(store, shiftSealer{}, cache, discardActivity{})
	accountID := uuid.New()
	actorID := uuid.New()
	ctx := context.Background()
	store.rows[store.key(accountID, groupAccountSecurity)] = map[string]string{keyRequire2FA: "true"}

	required, err := service.Require2FA(ctx, accountID)
	if err != nil || !required {
		t.Fatalf("first read = %v, %v", required, err)
	}
	securityKey := cacheKey(accountID, groupAccountSecurity)
	if cache.ttls[securityKey] != settingsCacheTTL {
		t.Fatalf("ttl = %s", cache.ttls[securityKey])
	}
	store.rows[store.key(accountID, groupAccountSecurity)][keyRequire2FA] = "false"
	reads := store.accountReads
	cached, err := service.Require2FA(ctx, accountID)
	if err != nil || !cached {
		t.Fatalf("cached read = %v, %v", cached, err)
	}
	if store.accountReads != reads {
		t.Fatal("cached require_2fa read the database")
	}

	if err := service.FlushSection(ctx, accountID, "owner", sectionSecurity); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if _, ok := cache.values[securityKey]; ok {
		t.Fatal("flush left the cache key")
	}
	afterFlush, err := service.Require2FA(ctx, accountID)
	if err != nil || afterFlush {
		t.Fatalf("read after flush = %v, %v", afterFlush, err)
	}

	if _, err := service.Save(ctx, accountID, actorID, "admin", groupAccountSecurity, map[string]any{
		keyRequire2FA: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := cache.values[securityKey]; ok {
		t.Fatal("save left the cache key")
	}
	afterSave, err := service.Require2FA(ctx, accountID)
	if err != nil || !afterSave {
		t.Fatalf("read after save = %v, %v", afterSave, err)
	}
	if _, ok := cache.values[securityKey]; !ok {
		t.Fatal("read after save missed the cache key")
	}

	if _, err := service.ResetSection(ctx, accountID, actorID, "owner", sectionSecurity); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, ok := cache.values[securityKey]; ok {
		t.Fatal("reset left the cache key")
	}
	afterReset, err := service.Require2FA(ctx, accountID)
	if err != nil || afterReset {
		t.Fatalf("read after reset = %v, %v", afterReset, err)
	}
}

func TestCorruptSealAndCacheFailureFallThroughToTheDatabase(t *testing.T) {
	t.Parallel()

	store := newCountingStore()
	cache := &memoryCache{}
	service := NewService(store, shiftSealer{}, cache, discardActivity{})
	accountID := uuid.New()
	ctx := context.Background()
	store.rows[store.key(accountID, groupAccountSweepLimits)] = map[string]string{
		keyMaxAddressesEVM: "7",
	}

	limits, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil || limits.MaxAddressesEVM != 7 {
		t.Fatalf("first limits = %+v, %v", limits, err)
	}
	limitsKey := cacheKey(accountID, groupAccountSweepLimits)
	cache.values[limitsKey] = "not-a-seal"
	store.rows[store.key(accountID, groupAccountSweepLimits)][keyMaxAddressesEVM] = "9"
	reads := store.accountReads
	fallen, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil || fallen.MaxAddressesEVM != 9 {
		t.Fatalf("corrupt seal = %+v, %v", fallen, err)
	}
	if store.accountReads != reads+1 {
		t.Fatal("corrupt seal did not read the database")
	}
	replaced := cachedJSONMap(t, cache.values[limitsKey])
	if replaced[keyMaxAddressesEVM] != "9" {
		t.Fatal("fallthrough did not replace the corrupt cache")
	}

	cache.getErr = errors.New("redis down")
	store.rows[store.key(accountID, groupAccountSweepLimits)][keyMaxAddressesEVM] = "11"
	failed, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil || failed.MaxAddressesEVM != 11 {
		t.Fatalf("cache failure = %+v, %v", failed, err)
	}

	cache.getErr = nil
	store.accountErr = errors.New("db down")
	cache.values[limitsKey] = "enc:v1:%%%"
	_, err = service.EffectiveSweepLimits(ctx, accountID)
	if err == nil || err.Error() != "db down" {
		t.Fatalf("database error = %v", err)
	}
}

func TestPlatformReadHitsTheJSONMapUntilItIsForgotten(t *testing.T) {
	t.Parallel()

	store := newCountingStore()
	cache := &memoryCache{}
	service := NewService(store, shiftSealer{}, cache, discardActivity{})
	ctx := context.Background()
	store.PutPlatform(groupDepositScan, map[string]string{keyBatchBlocks: "80"})

	first, err := service.EffectiveDepositScan(ctx)
	if err != nil || first.BatchBlocks != 80 {
		t.Fatalf("first scan = %+v, %v", first, err)
	}
	key := platformCacheKey(groupDepositScan)
	if key != "settings:platform:"+groupDepositScan {
		t.Fatalf("platform key = %s", key)
	}
	if cache.ttls[key] != 10*time.Minute || cachedJSONMap(t, cache.values[key])[keyBatchBlocks] != "80" {
		t.Fatal("platform cache was not a JSON map for 10 minutes")
	}
	store.rows[platformStoreKey(groupDepositScan)][keyBatchBlocks] = "40"
	reads := store.platformReads
	cached, err := service.EffectiveDepositScan(ctx)
	if err != nil || cached.BatchBlocks != 80 {
		t.Fatalf("cached scan = %+v, %v", cached, err)
	}
	if store.platformReads != reads {
		t.Fatal("cached platform read hit the database")
	}

	cache.values[key] = "enc:v1:%%%"
	fallen, err := service.EffectiveDepositScan(ctx)
	if err != nil || fallen.BatchBlocks != 40 {
		t.Fatalf("corrupt platform seal = %+v, %v", fallen, err)
	}
	if !cache.Forget(key) {
		t.Fatal("forget reported failure")
	}
	if _, ok := cache.values[key]; ok {
		t.Fatal("forget left the platform cache key")
	}
	store.rows[platformStoreKey(groupDepositScan)][keyBatchBlocks] = "15"
	afterForget, err := service.EffectiveDepositScan(ctx)
	if err != nil || afterForget.BatchBlocks != 15 {
		t.Fatalf("read after forget = %+v, %v", afterForget, err)
	}
}

func cachedJSONMap(t *testing.T, raw string) map[string]string {
	t.Helper()
	var decoded map[string]string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil || decoded == nil {
		t.Fatalf("cache payload is not a JSON map: %q", raw)
	}
	return decoded
}

func TestCacheReadFailureKeepsTheDatabaseError(t *testing.T) {
	t.Parallel()

	store := newCountingStore()
	store.accountErr = errors.New("db down")
	cache := &memoryCache{getErr: errors.New("redis down")}
	service := NewService(store, shiftSealer{}, cache, discardActivity{})

	_, err := service.Require2FA(context.Background(), uuid.New())
	if err == nil || err.Error() != "db down" {
		t.Fatalf("error = %v", err)
	}
}
