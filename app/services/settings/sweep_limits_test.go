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

func TestEffectiveSweepLimits_FallbackChain(t *testing.T) {
	child, ok := FindGroup(groupAccountSweepLimits)
	if !ok {
		t.Fatal("account_sweep_limits is not in the registry")
	}
	restore := UseForTest([]Group{
		{Name: child.Inherits, Scope: ScopePlatform},
		child,
	})
	defer restore()

	store := newCountingStore()
	service := newTestService(store)
	accountID := uuid.New()
	otherID := uuid.New()
	ctx := context.Background()

	missing, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("empty chain: %v", err)
	}
	if missing != DefaultSweepLimits() {
		t.Fatalf("empty chain = %+v", missing)
	}
	if store.platformReads == 0 {
		t.Fatal("the parent group was not read")
	}

	store.PutPlatform(child.Inherits, map[string]string{
		keyMaxAddressesEVM:     "200",
		keyDailyWithdrawCapUSD: "10.00",
	})
	fromPlatform, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("platform chain: %v", err)
	}
	if fromPlatform.MaxAddressesEVM != 200 || fromPlatform.DailyWithdrawCapUSD != "10.00" {
		t.Fatalf("platform chain = %+v", fromPlatform)
	}
	if fromPlatform.MaxAddressesSolana != defaultMaxAddressesSolana {
		t.Fatalf("solana = %d, want the registry default", fromPlatform.MaxAddressesSolana)
	}

	if err := store.UpsertMany(ctx, accountID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM: "7",
	}); err != nil {
		t.Fatalf("store account override: %v", err)
	}
	overridden, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("account override: %v", err)
	}
	if overridden.MaxAddressesEVM != 7 || overridden.DailyWithdrawCapUSD != "10.00" {
		t.Fatalf("account override = %+v", overridden)
	}

	other, err := service.EffectiveSweepLimits(ctx, otherID)
	if err != nil {
		t.Fatalf("other account: %v", err)
	}
	if other.MaxAddressesEVM != 200 {
		t.Fatalf("other account read %d from the first account", other.MaxAddressesEVM)
	}

	if err := store.UpsertMany(ctx, accountID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM: "nope",
	}); err != nil {
		t.Fatalf("store invalid account value: %v", err)
	}
	invalid, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("invalid account value: %v", err)
	}
	if invalid.MaxAddressesEVM != defaultMaxAddressesEVM {
		t.Fatalf("invalid account value = %d, want the registry default", invalid.MaxAddressesEVM)
	}

	store.platformErr = errors.New("db down")
	if _, err := service.EffectiveSweepLimits(ctx, otherID); err == nil || err.Error() != "db down" {
		t.Fatalf("platform error = %v", err)
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

func TestAccountSweepLimitsWireOmitsWhenNothingIsStored(t *testing.T) {
	t.Parallel()

	service := newTestService(newMemoryStore())
	got, err := service.AccountSweepLimitsWire(context.Background(), uuid.New())
	if err != nil || got != nil {
		t.Fatalf("wire = %v, %v; want nil", got, err)
	}
}

func TestAccountSweepLimitsWireUsesTheStoredOverrideAndRegistryDefaults(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()
	ctx := context.Background()
	if err := store.UpsertMany(ctx, accountID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM:     "40",
		keyDailyWithdrawCapUSD: "12.50",
	}); err != nil {
		t.Fatalf("store override: %v", err)
	}

	got, err := service.AccountSweepLimitsWire(ctx, accountID)
	if err != nil || got == nil {
		t.Fatalf("wire = %v, %v", got, err)
	}
	want := `{"max_addresses_evm":40,"max_addresses_solana":25,"max_addresses_bitcoin":100,"max_consolidate_requests_per_day":50,"daily_withdraw_cap_usd":"12.50"}`
	if *got != want {
		t.Fatalf("wire = %s, want %s", *got, want)
	}
}

func TestAccountSweepLimitsWireOmitsANegativeCap(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store)
	accountID := uuid.New()
	ctx := context.Background()
	if err := store.UpsertMany(ctx, accountID, groupAccountSweepLimits, map[string]string{
		keyDailyWithdrawCapUSD: "-1",
	}); err != nil {
		t.Fatalf("store override: %v", err)
	}

	got, err := service.AccountSweepLimitsWire(ctx, accountID)
	if err != nil || got == nil {
		t.Fatalf("wire = %v, %v", got, err)
	}
	if *got != `{"max_addresses_evm":100,"max_addresses_solana":25,"max_addresses_bitcoin":100,"max_consolidate_requests_per_day":50}` {
		t.Fatalf("wire = %s", *got)
	}
}

func TestMarshalSweepLimitsWireRefusesANegativeCap(t *testing.T) {
	t.Parallel()

	values := DefaultSweepLimits()
	values.DailyWithdrawCapUSD = "-1"
	if _, err := marshalSweepLimitsWire(values); err == nil {
		t.Fatal("expected a negative cap to be refused")
	}
}
