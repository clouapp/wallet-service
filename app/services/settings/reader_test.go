package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestReaderWithoutACacheReadsTheTable(t *testing.T) {
	t.Parallel()

	store := newCountingStore()
	service := NewService(store, prefixSealer{}, nil, discardActivity{})
	accountID := uuid.New()
	ctx := context.Background()
	store.rows[store.key(accountID, groupAccountSecurity)] = map[string]string{keyRequire2FA: "true"}

	for range 2 {
		required, err := service.Require2FA(ctx, accountID)
		if err != nil || !required {
			t.Fatalf("require_2fa = %v, %v", required, err)
		}
	}
	if store.accountReads != 2 {
		t.Fatalf("store reads = %d, want 2", store.accountReads)
	}
}

func TestReaderRefusesAGroupOutsideTheCatalog(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	_, err := service.Save(context.Background(), uuid.New(), uuid.New(), "owner", "nope", map[string]any{})
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestConsumersRereadAfterForget(t *testing.T) {
	t.Parallel()

	store := newCountingStore()
	cache := &memoryCache{}
	service := NewService(store, shiftSealer{}, cache, discardActivity{})
	accountID := uuid.New()
	ctx := context.Background()
	store.rows[store.key(accountID, groupAccountSecurity)] = map[string]string{keyRequire2FA: "true"}
	store.rows[store.key(accountID, groupAccountSweepLimits)] = map[string]string{keyMaxAddressesEVM: "4"}
	store.PutPlatform(groupDepositScan, map[string]string{keyBatchBlocks: "80"})

	required, err := service.Require2FA(ctx, accountID)
	if err != nil || !required {
		t.Fatalf("require_2fa = %v, %v", required, err)
	}
	limits, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil || limits.MaxAddressesEVM != 4 {
		t.Fatalf("limits = %+v, %v", limits, err)
	}
	scan, err := service.EffectiveDepositScan(ctx)
	if err != nil || scan.BatchBlocks != 80 {
		t.Fatalf("scan = %+v, %v", scan, err)
	}

	store.rows[store.key(accountID, groupAccountSecurity)][keyRequire2FA] = "false"
	store.rows[store.key(accountID, groupAccountSweepLimits)][keyMaxAddressesEVM] = "8"
	store.rows[platformStoreKey(groupDepositScan)][keyBatchBlocks] = "20"

	cachedRequired, err := service.Require2FA(ctx, accountID)
	if err != nil || !cachedRequired {
		t.Fatalf("cached require_2fa = %v, %v", cachedRequired, err)
	}
	cachedLimits, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil || cachedLimits.MaxAddressesEVM != 4 {
		t.Fatalf("cached limits = %+v, %v", cachedLimits, err)
	}
	cachedScan, err := service.EffectiveDepositScan(ctx)
	if err != nil || cachedScan.BatchBlocks != 80 {
		t.Fatalf("cached scan = %+v, %v", cachedScan, err)
	}

	securityKey := cacheKey(accountID, groupAccountSecurity)
	limitsKey := cacheKey(accountID, groupAccountSweepLimits)
	scanKey := platformCacheKey(groupDepositScan)
	for _, key := range []string{securityKey, limitsKey, scanKey} {
		if !cache.Forget(key) {
			t.Fatalf("forget %s reported failure", key)
		}
	}

	afterRequired, err := service.Require2FA(ctx, accountID)
	if err != nil || afterRequired {
		t.Fatalf("require_2fa after forget = %v, %v", afterRequired, err)
	}
	afterLimits, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil || afterLimits.MaxAddressesEVM != 8 {
		t.Fatalf("limits after forget = %+v, %v", afterLimits, err)
	}
	afterScan, err := service.EffectiveDepositScan(ctx)
	if err != nil || afterScan.BatchBlocks != 20 {
		t.Fatalf("scan after forget = %+v, %v", afterScan, err)
	}
}
