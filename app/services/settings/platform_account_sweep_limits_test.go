package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSavePlatformAccountSweepLimits_AccountRowOverridesThePlatformRow(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	accountID := uuid.New()
	otherID := uuid.New()
	store := newMemoryStore()
	store.PutPlatform(groupSweepLimits, map[string]string{
		keyMaxAddressesEVM:     "40",
		keyMaxAddressesSolana:  "12",
		keyDailyWithdrawCapUSD: "9.50",
	})
	if err := store.UpsertMany(context.Background(), otherID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM: "19",
	}); err != nil {
		t.Fatalf("store other: %v", err)
	}
	activity := &recordingActivity{}
	cache := &memoryCache{}
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: cache, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}}).
		WithAccounts(&setAccounts{ids: map[uuid.UUID]bool{accountID: true, otherID: true}})
	ctx := context.Background()

	before, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("before: %v", err)
	}
	if before.MaxAddressesEVM != 40 || before.MaxAddressesSolana != 12 || before.DailyWithdrawCapUSD != "9.50" {
		t.Fatalf("platform row did not apply before the account write: %+v", before)
	}

	view, err := service.SavePlatformAccountSweepLimits(ctx, actor, accountID, "  "+groupAccountSweepLimits+"  ", map[string]any{
		keyMaxAddressesEVM:     7,
		keyDailyWithdrawCapUSD: "",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if view.Name != groupAccountSweepLimits || view.Scope != ScopeAccount || view.ManagedBy != ManagedByPlatform || !view.CanUpdate {
		t.Fatalf("view = %+v", view)
	}
	evm := fieldByKey(t, view, keyMaxAddressesEVM)
	if evm.Value != 7 || !evm.IsSet {
		t.Fatalf("evm = %+v", evm)
	}
	capField := fieldByKey(t, view, keyDailyWithdrawCapUSD)
	if capField.Value != "" || !capField.IsSet {
		t.Fatalf("cap = %+v", capField)
	}
	storedEVM, ok := store.get(accountID, groupAccountSweepLimits, keyMaxAddressesEVM)
	if !ok || storedEVM != "7" {
		t.Fatalf("stored evm = %q present %v", storedEVM, ok)
	}
	storedCap, ok := store.get(accountID, groupAccountSweepLimits, keyDailyWithdrawCapUSD)
	if !ok || storedCap != "" {
		t.Fatalf("stored cap = %q present %v, want an empty unlimited cap", storedCap, ok)
	}
	otherEVM, ok := store.get(otherID, groupAccountSweepLimits, keyMaxAddressesEVM)
	if !ok || otherEVM != "19" {
		t.Fatalf("other account evm = %q present %v", otherEVM, ok)
	}
	platformEVM, ok := platformStored(t, store, keyMaxAddressesEVM)
	if !ok || platformEVM != "40" {
		t.Fatalf("platform evm = %q present %v", platformEVM, ok)
	}

	wantKey := cacheKey(accountID, groupAccountSweepLimits)
	if len(cache.keys) != 1 || cache.keys[0] != wantKey {
		t.Fatalf("forgotten keys = %v, want %s", cache.keys, wantKey)
	}
	if _, stillCached := cache.values[wantKey]; stillCached {
		t.Fatal("the account cache key was left in place")
	}

	after, err := service.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	if after.MaxAddressesEVM != 7 || after.MaxAddressesSolana != 12 || after.DailyWithdrawCapUSD != "" {
		t.Fatalf("account row did not win: %+v", after)
	}
	if len(activity.rows) != 1 {
		t.Fatalf("activity rows = %d", len(activity.rows))
	}
	row := activity.rows[0]
	if row.AccountID == nil || *row.AccountID != accountID || row.Action != "settings.updated" || row.TargetType != "settings" || row.TargetID != groupAccountSweepLimits {
		t.Fatalf("activity = %+v", row)
	}
	encoded, err := row.Metadata.Encode()
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	want := `{"fields":["daily_withdraw_cap_usd","max_addresses_evm"],"group":"account_sweep_limits"}`
	if encoded != want {
		t.Fatalf("metadata = %s", encoded)
	}
	if strings.Contains(encoded, "7") || strings.Contains(encoded, "9.50") || strings.Contains(encoded, "40") {
		t.Fatalf("metadata stored a value: %s", encoded)
	}

	if _, err := service.SavePlatformAccountSweepLimits(ctx, actor, accountID, groupAccountSweepLimits, map[string]any{
		keyDailyWithdrawCapUSD: "0",
	}); err != nil {
		t.Fatalf("zero cap: %v", err)
	}
	zeroCap, ok := store.get(accountID, groupAccountSweepLimits, keyDailyWithdrawCapUSD)
	if !ok || zeroCap != "0" {
		t.Fatalf("stored zero cap = %q present %v", zeroCap, ok)
	}
}

func TestSavePlatformAccountSweepLimits_NotFoundComesBeforeForbidden(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	accountID := uuid.New()
	admins := &countingPlatformAdmins{}
	accounts := &setAccounts{ids: map[uuid.UUID]bool{accountID: true}}
	store := &recordingAccountStore{memoryStore: newMemoryStore()}
	if err := store.UpsertMany(context.Background(), accountID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM: "17",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: &memoryCache{}, Activity: &recordingActivity{}}).
		WithPlatformAdmins(admins).
		WithAccounts(accounts)
	ctx := context.Background()

	for _, name := range []string{"no-such-group", groupSweepLimits, groupMailSMTP, groupAccountSecurity, groupAccountWebhooks} {
		_, err := service.SavePlatformAccountSweepLimits(ctx, actor, accountID, name, map[string]any{
			keyMaxAddressesEVM: 7,
		})
		if !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("%s = %v, want group not found", name, err)
		}
	}
	if accounts.calls != 0 || admins.calls != 0 || len(store.listed) != 0 {
		t.Fatalf("ineligible group looked up account %d admin %d rows %d", accounts.calls, admins.calls, len(store.listed))
	}

	_, err := service.SavePlatformAccountSweepLimits(ctx, actor, uuid.Nil, groupAccountSweepLimits, map[string]any{keyMaxAddressesEVM: 7})
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("nil account = %v", err)
	}
	_, err = service.SavePlatformAccountSweepLimits(ctx, actor, uuid.New(), groupAccountSweepLimits, map[string]any{keyMaxAddressesEVM: 7})
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("unknown account = %v", err)
	}
	if admins.calls != 0 || len(store.listed) != 0 {
		t.Fatalf("unknown account checked admin %d or listed %d", admins.calls, len(store.listed))
	}

	_, err = service.SavePlatformAccountSweepLimits(ctx, actor, accountID, groupAccountSweepLimits, map[string]any{keyMaxAddressesEVM: 7})
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin = %v", err)
	}
	if len(store.listed) != 0 {
		t.Fatalf("non-admin listed %d rows", len(store.listed))
	}
	stored, ok := store.get(accountID, groupAccountSweepLimits, keyMaxAddressesEVM)
	if !ok || stored != "17" {
		t.Fatalf("stored evm = %q present %v", stored, ok)
	}
	if len(service.activity.(*recordingActivity).rows) != 0 {
		t.Fatal("a forbidden write recorded activity")
	}
}

func TestSavePlatformAccountSweepLimits_ZeroNegativeAndANegativeCapAreNotStored(t *testing.T) {
	t.Parallel()

	actor := uuid.New()
	accountID := uuid.New()
	store := newMemoryStore()
	if err := store.UpsertMany(context.Background(), accountID, groupAccountSweepLimits, map[string]string{
		keyMaxAddressesEVM:     "17",
		keyDailyWithdrawCapUSD: "1.25",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	activity := &recordingActivity{}
	cache := &memoryCache{}
	service := NewService(Deps{Store: store, Sealer: prefixSealer{}, Cache: cache, Activity: activity}).
		WithPlatformAdmins(allowPlatformAdmins{ids: map[uuid.UUID]bool{actor: true}}).
		WithAccounts(&setAccounts{ids: map[uuid.UUID]bool{accountID: true}})
	ctx := context.Background()

	for _, body := range []map[string]any{
		{keyMaxAddressesEVM: 0, keyMaxAddressesSolana: 12, keyMaxAddressesBitcoin: 30, keyMaxConsolidateRequestsPerDay: 8},
		{keyMaxAddressesEVM: -2, keyMaxAddressesSolana: 12, keyMaxAddressesBitcoin: 30, keyMaxConsolidateRequestsPerDay: 8},
		{keyMaxAddressesEVM: 40, keyMaxAddressesSolana: 12, keyMaxAddressesBitcoin: 30, keyMaxConsolidateRequestsPerDay: 8, keyDailyWithdrawCapUSD: "-1.50"},
	} {
		_, err := service.SavePlatformAccountSweepLimits(ctx, actor, accountID, groupAccountSweepLimits, body)
		validation, ok := err.(*ValidationError)
		if !ok || validation.empty() {
			t.Fatalf("body %#v error = %v, want validation", body, err)
		}
	}
	evm, ok := store.get(accountID, groupAccountSweepLimits, keyMaxAddressesEVM)
	if !ok || evm != "17" {
		t.Fatalf("stored evm = %q present %v", evm, ok)
	}
	capUSD, ok := store.get(accountID, groupAccountSweepLimits, keyDailyWithdrawCapUSD)
	if !ok || capUSD != "1.25" {
		t.Fatalf("stored cap = %q present %v", capUSD, ok)
	}
	for key, value := range store.rows[store.key(accountID, groupAccountSweepLimits)] {
		if strings.HasPrefix(value, "-") {
			t.Fatalf("stored a negative %s=%q", key, value)
		}
	}
	if len(activity.rows) != 0 {
		t.Fatalf("activity rows = %d, want none", len(activity.rows))
	}
	if len(cache.keys) != 0 {
		t.Fatalf("a rejected write forgot %v", cache.keys)
	}
}
