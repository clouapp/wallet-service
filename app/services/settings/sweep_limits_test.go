package settings

import (
	"context"
	"errors"
	"strings"
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
	if err := store.UpsertMany(ctx, otherID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesSolana: "9",
	}); err != nil {
		t.Fatalf("store account override during platform outage: %v", err)
	}
	duringOutage, err := service.EffectiveSweepLimits(ctx, otherID)
	if err != nil {
		t.Fatalf("platform outage = %v", err)
	}
	if duringOutage.MaxAddressesSolana != 9 {
		t.Fatalf("account override during platform outage = %d", duringOutage.MaxAddressesSolana)
	}
	if duringOutage.MaxAddressesEVM != defaultMaxAddressesEVM || duringOutage.DailyWithdrawCapUSD != "" {
		t.Fatalf("platform outage kept platform values: %+v", duringOutage)
	}
}

func TestEffectiveSweepLimits_ReadsThePlatformGroupUnderTheAccountOverride(t *testing.T) {
	t.Parallel()

	store := newCountingStore()
	service := newTestService(store)
	accountID := uuid.New()
	ctx := context.Background()

	missing, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("missing platform row: %v", err)
	}
	if missing != DefaultSweepLimits() {
		t.Fatalf("missing platform row = %+v", missing)
	}
	if store.platformReads == 0 {
		t.Fatal("sweep_limits was not read")
	}

	store.PutPlatform(groupSweepLimits, map[string]string{
		keyMaxAddressesEVM:     "0",
		keyMaxAddressesSolana:  "-3",
		keyMaxAddressesBitcoin: "30",
		keyDailyWithdrawCapUSD: "-1",
	})
	invalid, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("invalid platform row: %v", err)
	}
	if invalid.MaxAddressesEVM != defaultMaxAddressesEVM || invalid.MaxAddressesSolana != defaultMaxAddressesSolana {
		t.Fatalf("invalid platform counts = %+v", invalid)
	}
	if invalid.MaxAddressesBitcoin != 30 || invalid.DailyWithdrawCapUSD != "" {
		t.Fatalf("invalid platform row = %+v", invalid)
	}

	store.PutPlatform(groupSweepLimits, map[string]string{
		keyMaxAddressesEVM:              "200",
		keyMaxAddressesSolana:           "12",
		keyMaxConsolidateRequestsPerDay: "8",
		keyDailyWithdrawCapUSD:          "10.00",
	})
	fromPlatform, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("platform row: %v", err)
	}
	if fromPlatform.MaxAddressesEVM != 200 || fromPlatform.MaxAddressesSolana != 12 ||
		fromPlatform.MaxConsolidateRequestsPerDay != 8 || fromPlatform.DailyWithdrawCapUSD != "10.00" {
		t.Fatalf("platform row = %+v", fromPlatform)
	}
	if fromPlatform.MaxAddressesBitcoin != defaultMaxAddressesBitcoin {
		t.Fatalf("bitcoin = %d, want the registry default", fromPlatform.MaxAddressesBitcoin)
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
	if overridden.MaxAddressesEVM != 7 || overridden.MaxAddressesSolana != 12 || overridden.DailyWithdrawCapUSD != "10.00" {
		t.Fatalf("account override = %+v", overridden)
	}

	store.platformErr = errors.New("db down")
	duringOutage, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("platform outage = %v", err)
	}
	if duringOutage.MaxAddressesEVM != 7 || duringOutage.MaxAddressesSolana != defaultMaxAddressesSolana ||
		duringOutage.DailyWithdrawCapUSD != "" {
		t.Fatalf("platform outage = %+v", duringOutage)
	}
}

func TestSavePlatformSweepLimits_RejectsZeroNegativeAndANegativeCap(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()

	for _, body := range []map[string]any{
		{keyMaxAddressesEVM: 0, keyMaxAddressesSolana: 12, keyMaxAddressesBitcoin: 30, keyMaxConsolidateRequestsPerDay: 8},
		{keyMaxAddressesEVM: -1, keyMaxAddressesSolana: 12, keyMaxAddressesBitcoin: 30, keyMaxConsolidateRequestsPerDay: 8},
		{keyMaxAddressesEVM: 40, keyMaxAddressesSolana: 12, keyMaxAddressesBitcoin: 30, keyMaxConsolidateRequestsPerDay: 8, keyDailyWithdrawCapUSD: "-1"},
	} {
		_, err := service.SavePlatform(ctx, actor, groupSweepLimits, body)
		validation, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("body %#v error = %v, want validation", body, err)
		}
		if len(validation.Fields) == 0 {
			t.Fatalf("body %#v stored a rejection with no fields", body)
		}
	}
	rows, err := store.ListPlatform(ctx, groupSweepLimits)
	if err != nil || len(rows) != 0 {
		t.Fatalf("stored rows = %+v, %v", rows, err)
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want none", len(activity.rows))
	}
}

func TestSavePlatformSweepLimits_BlankCapStaysEmptyAndIsWhatTheReaderReturns(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	activity := &recordingActivity{}
	actor := uuid.New()
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}})
	ctx := context.Background()
	accountID := uuid.New()

	view, err := service.SavePlatform(ctx, actor, groupSweepLimits, map[string]any{
		keyMaxAddressesEVM:              40,
		keyMaxAddressesSolana:           12,
		keyMaxAddressesBitcoin:          30,
		keyMaxConsolidateRequestsPerDay: 8,
		keyDailyWithdrawCapUSD:          "",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if view.Name != groupSweepLimits || !view.CanUpdate {
		t.Fatalf("view = %+v", view)
	}
	capUSD, ok := platformStored(t, store, keyDailyWithdrawCapUSD)
	if !ok || capUSD != "" {
		t.Fatalf("stored cap = %q present %v, want a blank unlimited cap", capUSD, ok)
	}
	got, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got.MaxAddressesEVM != 40 || got.DailyWithdrawCapUSD != "" {
		t.Fatalf("effective = %+v", got)
	}
	if len(activity.rows) != 1 {
		t.Fatalf("activity rows = %d", len(activity.rows))
	}
	row := activity.rows[0]
	if row.AccountID != nil || row.Action != "settings.updated" || row.TargetType != "settings" || row.TargetID != groupSweepLimits {
		t.Fatalf("activity = %+v", row)
	}
	encoded, err := row.Metadata.Encode()
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	want := `{"fields":["daily_withdraw_cap_usd","max_addresses_bitcoin","max_addresses_evm","max_addresses_solana","max_consolidate_requests_per_day"],"group":"sweep_limits"}`
	if encoded != want {
		t.Fatalf("metadata = %s", encoded)
	}
	if strings.Contains(encoded, "40") {
		t.Fatalf("metadata stored a value: %s", encoded)
	}

	if _, err := service.SavePlatform(ctx, actor, groupSweepLimits, map[string]any{
		keyDailyWithdrawCapUSD: "0",
	}); err != nil {
		t.Fatalf("zero cap: %v", err)
	}
	zeroCap, ok := platformStored(t, store, keyDailyWithdrawCapUSD)
	if !ok || zeroCap != "0" {
		t.Fatalf("stored zero cap = %q present %v", zeroCap, ok)
	}
	withZero, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil || withZero.DailyWithdrawCapUSD != "0" {
		t.Fatalf("zero cap effective = %+v, %v", withZero, err)
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

func platformStored(t *testing.T, store *memoryStore, key string) (string, bool) {
	t.Helper()
	rows, err := store.ListPlatform(context.Background(), groupSweepLimits)
	if err != nil {
		t.Fatalf("list platform: %v", err)
	}
	for _, row := range rows {
		if row.Key == key {
			return row.Value, true
		}
	}
	return "", false
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
