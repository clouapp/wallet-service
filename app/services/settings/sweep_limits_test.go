package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestEffectiveSweepLimits_UsesRegistryDefaultsWhenNothingIsStored(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	got, err := service.EffectiveSweepLimits(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("effective limits: %v", err)
	}
	if got != DefaultSweepLimits() {
		t.Fatalf("effective = %+v, want registry defaults %+v", got, DefaultSweepLimits())
	}
}

func TestEffectiveSweepLimits_StoredRowOverridesOneKey(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()
	ctx := context.Background()
	if err := store.UpsertMany(ctx, accountID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM: "500",
	}); err != nil {
		t.Fatalf("store override: %v", err)
	}
	if err := store.UpsertMany(ctx, accountID, groupAccountSecurity, map[string]string{
		keyRequire2FA: "true",
	}); err != nil {
		t.Fatalf("store unrelated setting: %v", err)
	}

	got, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("effective limits: %v", err)
	}
	if got.MaxAddressesEVM != 500 {
		t.Fatalf("evm = %d, want 500", got.MaxAddressesEVM)
	}
	want := DefaultSweepLimits()
	want.MaxAddressesEVM = 500
	if got != want {
		t.Fatalf("effective = %+v, want %+v", got, want)
	}
}

func TestEffectiveSweepLimits_InvalidKeyFallsBackToThatDefault(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()
	ctx := context.Background()
	if err := store.UpsertMany(ctx, accountID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesSolana:           "0",
		keyMaxConsolidateRequestsPerDay: "nope",
		keyDailyWithdrawCapUSD:          "-1",
		keyMaxAddressesEVM:              "40",
	}); err != nil {
		t.Fatalf("store override: %v", err)
	}

	got, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("effective limits: %v", err)
	}
	if got.MaxAddressesEVM != 40 {
		t.Fatalf("evm = %d, want the stored 40", got.MaxAddressesEVM)
	}
	if got.MaxAddressesSolana != defaultMaxAddressesSolana {
		t.Fatalf("solana = %d, want default %d", got.MaxAddressesSolana, defaultMaxAddressesSolana)
	}
	if got.MaxConsolidateRequestsPerDay != defaultMaxConsolidateRequestsPerDay {
		t.Fatalf("quota = %d, want default %d", got.MaxConsolidateRequestsPerDay, defaultMaxConsolidateRequestsPerDay)
	}
	if got.DailyWithdrawCapUSD != "" {
		t.Fatalf("cap = %q, want blank", got.DailyWithdrawCapUSD)
	}
	if got.MaxAddressesBitcoin != defaultMaxAddressesBitcoin {
		t.Fatalf("bitcoin = %d, want default %d", got.MaxAddressesBitcoin, defaultMaxAddressesBitcoin)
	}
}

func TestEffectiveSweepLimits_DoesNotReadThePlatformGroup(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	store := &groupGuardStore{allow: groupAccountSweepLimits, inner: newMemoryStore()}
	service := newTestService(store)
	if _, err := service.EffectiveSweepLimits(context.Background(), accountID); err != nil {
		t.Fatalf("effective limits: %v", err)
	}
}

func TestEffectiveSweepLimits_StoreError(t *testing.T) {
	t.Parallel()

	service := newTestService(errStore{err: errors.New("db down")})
	_, err := service.EffectiveSweepLimits(context.Background(), uuid.New())
	if err == nil || err.Error() != "db down" {
		t.Fatalf("error = %v, want db down", err)
	}
}

func TestEffectiveSweepLimits_RejectsANilAccount(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	_, err := service.EffectiveSweepLimits(context.Background(), uuid.Nil)
	if err == nil {
		t.Fatal("expected an error for a nil account")
	}
}

func TestDefaultSweepLimitsMatchTheRegistry(t *testing.T) {
	t.Parallel()

	group, ok := FindGroup(groupAccountSweepLimits)
	if !ok {
		t.Fatal("account_sweep_limits is not in the registry")
	}
	defaults := DefaultSweepLimits()
	want := map[string]string{
		keyMaxAddressesEVM:              "100",
		keyMaxAddressesSolana:           "25",
		keyMaxAddressesBitcoin:          "100",
		keyMaxConsolidateRequestsPerDay: "50",
		keyDailyWithdrawCapUSD:          "",
	}
	if len(group.Settings) != len(want) {
		t.Fatalf("registry has %d keys, the sweep reader knows %d", len(group.Settings), len(want))
	}
	for _, definition := range group.Settings {
		stored := defaultStored(definition)
		expected, known := want[definition.Key]
		if !known {
			t.Fatalf("registry key %q is not applied by the sweep reader", definition.Key)
		}
		if stored != expected {
			t.Fatalf("default %s = %q, want %q", definition.Key, stored, expected)
		}
	}
	if defaults.MaxAddressesEVM != 100 || defaults.MaxAddressesSolana != 25 ||
		defaults.MaxAddressesBitcoin != 100 || defaults.MaxConsolidateRequestsPerDay != 50 ||
		defaults.DailyWithdrawCapUSD != "" {
		t.Fatalf("DefaultSweepLimits = %+v", defaults)
	}
}

type groupGuardStore struct {
	allow string
	inner *memoryStore
}

func (s *groupGuardStore) ListGroup(ctx context.Context, accountID uuid.UUID, group string) ([]models.Setting, error) {
	if group != s.allow {
		return nil, errors.New("read " + group)
	}
	return s.inner.ListGroup(ctx, accountID, group)
}

func (s *groupGuardStore) UpsertMany(ctx context.Context, accountID uuid.UUID, group string, values map[string]string) error {
	if group != s.allow {
		return errors.New("write " + group)
	}
	return s.inner.UpsertMany(ctx, accountID, group, values)
}

type errStore struct {
	err error
}

func (s errStore) ListGroup(context.Context, uuid.UUID, string) ([]models.Setting, error) {
	return nil, s.err
}

func (s errStore) UpsertMany(context.Context, uuid.UUID, string, map[string]string) error {
	return s.err
}
