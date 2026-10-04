package features

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type memoryStore struct {
	rows   map[uuid.UUID]map[string]bool
	global map[string]bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		rows:   map[uuid.UUID]map[string]bool{},
		global: map[string]bool{},
	}
}

type memoryAdmins struct {
	users map[uuid.UUID]struct{}
}

func (a memoryAdmins) Contains(_ context.Context, userID uuid.UUID) (bool, error) {
	if a.users == nil {
		return false, nil
	}
	_, ok := a.users[userID]
	return ok, nil
}

func (s *memoryStore) ListAccount(_ context.Context, accountID uuid.UUID) ([]models.Feature, error) {
	values := s.rows[accountID]
	rows := make([]models.Feature, 0, len(values))
	for key, enabled := range values {
		rows = append(rows, models.Feature{AccountID: accountID, Key: key, Enabled: enabled})
	}
	return rows, nil
}

func (s *memoryStore) Upsert(_ context.Context, accountID uuid.UUID, key string, enabled bool) error {
	if s.rows[accountID] == nil {
		s.rows[accountID] = map[string]bool{}
	}
	s.rows[accountID][key] = enabled
	return nil
}

func (s *memoryStore) written(accountID uuid.UUID, key string) (bool, bool) {
	value, ok := s.rows[accountID][key]
	return value, ok
}

func (s *memoryStore) ListGlobal(context.Context) ([]models.GlobalFeature, error) {
	rows := make([]models.GlobalFeature, 0, len(s.global))
	for key, enabled := range s.global {
		rows = append(rows, models.GlobalFeature{Key: key, Enabled: enabled})
	}
	return rows, nil
}

func (s *memoryStore) GetGlobal(_ context.Context, key string) (bool, bool, error) {
	value, ok := s.global[key]
	return value, ok, nil
}

func (s *memoryStore) UpsertGlobal(_ context.Context, key string, enabled bool) error {
	if s.global == nil {
		s.global = map[string]bool{}
	}
	s.global[key] = enabled
	return nil
}

func (s *memoryStore) globalWritten(key string) (bool, bool) {
	value, ok := s.global[key]
	return value, ok
}

type discardActivity struct{}

func (discardActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (discardActivity) Append(context.Context, models.AccountActivity) error { return nil }

func newTestService(store Store, admins PlatformAdmins) *Service {
	return NewService(store, admins, discardActivity{})
}

func TestListMissingRowUsesCatalogDefaultAndWritesNothing(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()

	view, err := service.List(context.Background(), accountID, "auditor")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(view.Features) != len(All()) {
		t.Fatalf("features = %d, want %d", len(view.Features), len(All()))
	}
	for _, flag := range view.Features {
		definition, ok := Find(flag.Key)
		if !ok {
			t.Fatalf("list returned unknown key %s", flag.Key)
		}
		if flag.Enabled != definition.Default {
			t.Fatalf("%s enabled = %v, catalog default %v", flag.Key, flag.Enabled, definition.Default)
		}
	}
	if len(store.rows[accountID]) != 0 {
		t.Fatalf("list inserted %d rows", len(store.rows[accountID]))
	}
}

func TestSetThenListReadsTheStoredBoolean(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()

	written, err := service.Set(ctx, accountID, uuid.New(), "owner", FlagWithdrawalsEnabled, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !written.Enabled || written.Key != FlagWithdrawalsEnabled {
		t.Fatalf("write response = %+v", written)
	}

	view, err := service.List(ctx, accountID, "admin")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !flagEnabled(t, view, FlagWithdrawalsEnabled) {
		t.Fatal("enabled flag read back as disabled")
	}
	for _, flag := range view.Features {
		if flag.Key == FlagWithdrawalsEnabled {
			continue
		}
		definition, ok := Find(flag.Key)
		if !ok || flag.Enabled != definition.Default {
			t.Fatalf("%s changed without a write: %+v", flag.Key, flag)
		}
	}

	written, err = service.Set(ctx, accountID, uuid.New(), "admin", FlagWithdrawalsEnabled, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if written.Enabled {
		t.Fatal("disable response is still enabled")
	}
	view, err = service.List(ctx, accountID, "owner")
	if err != nil {
		t.Fatalf("list after disable: %v", err)
	}
	if flagEnabled(t, view, FlagWithdrawalsEnabled) {
		t.Fatal("disabled flag read back as enabled")
	}
}

func TestSetRejectsUnknownKeyAndAuditorBeforeWriting(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	ctx := context.Background()

	_, err := service.Set(ctx, accountID, uuid.New(), "owner", "not-a-flag", true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key error = %v", err)
	}
	if _, ok := store.written(accountID, "not-a-flag"); ok {
		t.Fatal("unknown key was stored")
	}

	_, err = service.Set(ctx, accountID, uuid.New(), "auditor", FlagSweepEnabled, true)
	if !errors.Is(err, ErrUpdateForbidden) {
		t.Fatalf("auditor error = %v", err)
	}
	_, err = service.Set(ctx, accountID, uuid.New(), "user", FlagSweepEnabled, true)
	if !errors.Is(err, ErrUpdateForbidden) {
		t.Fatalf("user error = %v", err)
	}
	if _, ok := store.written(accountID, FlagSweepEnabled); ok {
		t.Fatal("forbidden write was stored")
	}

	_, err = service.Set(ctx, accountID, uuid.New(), "user", "not-a-flag", true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key for a user = %v, want not found before forbidden", err)
	}
}

func TestListHidesFlagsFromAUserAndFromAnotherAccount(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	otherID := uuid.New()
	ctx := context.Background()

	if _, err := service.List(ctx, accountID, "user"); !errors.Is(err, ErrViewForbidden) {
		t.Fatalf("user list error = %v", err)
	}
	if _, err := service.Set(ctx, accountID, uuid.New(), "owner", FlagWalletCreationEnabled, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	other, err := service.List(ctx, otherID, "auditor")
	if err != nil {
		t.Fatalf("other account: %v", err)
	}
	if _, stored := store.written(otherID, FlagWalletCreationEnabled); stored {
		t.Fatal("another account inherited the flag")
	}
	definition, ok := Find(FlagWalletCreationEnabled)
	if !ok || flagEnabled(t, other, FlagWalletCreationEnabled) != definition.Default {
		t.Fatal("another account must use the catalog default")
	}

	if _, err := service.List(nil, accountID, "owner"); err == nil {
		t.Fatal("nil context was accepted")
	}
	if _, err := service.Set(ctx, uuid.Nil, uuid.New(), "owner", FlagSweepEnabled, true); err == nil {
		t.Fatal("nil account id was accepted")
	}
}

func TestPlatformListAndSetRequireAnAdminAndUseTheCatalogDefault(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	userID := uuid.New()
	admins := memoryAdmins{users: map[uuid.UUID]struct{}{userID: {}}}
	service := newTestService(store, admins)
	ctx := context.Background()

	if _, err := service.ListGlobal(ctx, uuid.New()); !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin list error = %v", err)
	}
	if _, err := service.SetGlobal(ctx, uuid.New(), FlagWithdrawalsEnabled, false); !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin set error = %v", err)
	}
	if _, err := service.SetGlobal(ctx, uuid.New(), "not-a-flag", false); !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin unknown key = %v, want forbidden before not found", err)
	}
	if _, ok := store.globalWritten(FlagWithdrawalsEnabled); ok {
		t.Fatal("a non-admin write was stored")
	}

	view, err := service.ListGlobal(ctx, userID)
	if err != nil {
		t.Fatalf("admin list: %v", err)
	}
	if len(view.Features) != len(ForGlobal()) {
		t.Fatalf("features = %d, want %d", len(view.Features), len(ForGlobal()))
	}
	for _, flag := range view.Features {
		definition, ok := Find(flag.Key)
		if !ok || flag.Enabled != definition.Default {
			t.Fatalf("%s enabled = %v, catalog default %v", flag.Key, flag.Enabled, definition.Default)
		}
	}
	if len(store.global) != 0 {
		t.Fatal("list inserted a global row")
	}

	written, err := service.SetGlobal(ctx, userID, FlagWithdrawalsEnabled, false)
	if err != nil {
		t.Fatalf("set off: %v", err)
	}
	if written.Enabled || written.Key != FlagWithdrawalsEnabled {
		t.Fatalf("write response = %+v", written)
	}
	if _, err := service.SetGlobal(ctx, userID, "not-a-flag", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("admin unknown key = %v", err)
	}
}

func TestActiveGlobalUsesTheCatalogDefaultAndIgnoresAccountRows(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	if err := store.Upsert(context.Background(), accountID, FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("account withdrawals: %v", err)
	}
	if err := store.Upsert(context.Background(), accountID, FlagWalletCreationEnabled, true); err != nil {
		t.Fatalf("account wallet creation: %v", err)
	}

	names, err := service.ActiveGlobal(context.Background())
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	want := []string{FlagDepositScanEnabled, FlagSweepEnabled, FlagWalletCreationEnabled, FlagWebhookDeliveryEnabled, FlagWithdrawalsEnabled}
	if !slices.Equal(names, want) {
		t.Fatalf("active = %v, want %v", names, want)
	}
	if len(store.global) != 0 {
		t.Fatal("active inserted a global row")
	}

	if err := store.UpsertGlobal(context.Background(), FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("global withdrawals: %v", err)
	}
	if err := store.UpsertGlobal(context.Background(), FlagUser2FARequired, true); err != nil {
		t.Fatalf("global 2fa: %v", err)
	}
	names, err = service.ActiveGlobal(context.Background())
	if err != nil {
		t.Fatalf("active after write: %v", err)
	}
	want = []string{FlagDepositScanEnabled, FlagSweepEnabled, FlagUser2FARequired, FlagWalletCreationEnabled, FlagWebhookDeliveryEnabled}
	if !slices.Equal(names, want) {
		t.Fatalf("active after write = %v, want %v", names, want)
	}

	if _, err := service.ActiveGlobal(nil); err == nil {
		t.Fatal("nil context must fail")
	}
}

type failingGlobalStore struct {
	*memoryStore
	err error
}

func (s failingGlobalStore) ListGlobal(context.Context) ([]models.GlobalFeature, error) {
	return nil, s.err
}

func TestActiveForAccountUsesTheCatalogDefaultAndIgnoresGlobalRows(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	service := newTestService(store, memoryAdmins{})
	accountID := uuid.New()
	if err := store.UpsertGlobal(context.Background(), FlagSweepEnabled, false); err != nil {
		t.Fatalf("global sweep: %v", err)
	}
	if err := store.UpsertGlobal(context.Background(), FlagUser2FARequired, true); err != nil {
		t.Fatalf("global 2fa: %v", err)
	}

	names, err := service.ActiveForAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	want := []string{FlagDepositScanEnabled, FlagSweepEnabled, FlagWalletCreationEnabled, FlagWebhookDeliveryEnabled, FlagWithdrawalsEnabled}
	if !slices.Equal(names, want) {
		t.Fatalf("active = %v, want %v", names, want)
	}
	if len(store.rows[accountID]) != 0 {
		t.Fatal("active inserted an account row")
	}

	if err := store.Upsert(context.Background(), accountID, FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("account withdrawals: %v", err)
	}
	if err := store.Upsert(context.Background(), accountID, FlagUser2FARequired, true); err != nil {
		t.Fatalf("account 2fa: %v", err)
	}
	names, err = service.ActiveForAccount(context.Background(), accountID)
	if err != nil {
		t.Fatalf("active after write: %v", err)
	}
	want = []string{FlagDepositScanEnabled, FlagSweepEnabled, FlagUser2FARequired, FlagWalletCreationEnabled, FlagWebhookDeliveryEnabled}
	if !slices.Equal(names, want) {
		t.Fatalf("active after write = %v, want %v", names, want)
	}

	if _, err := service.ActiveForAccount(nil, accountID); err == nil {
		t.Fatal("nil context must fail")
	}
	if _, err := service.ActiveForAccount(context.Background(), uuid.Nil); err == nil {
		t.Fatal("nil account must fail")
	}
	if _, err := (*Service)(nil).ActiveForAccount(context.Background(), accountID); err == nil {
		t.Fatal("nil service must fail")
	}
}

type failingAccountStore struct {
	*memoryStore
	err error
}

func (s failingAccountStore) ListAccount(context.Context, uuid.UUID) ([]models.Feature, error) {
	return nil, s.err
}

func TestActiveForAccountReportsAStoreError(t *testing.T) {
	t.Parallel()

	want := errors.New("account features unavailable")
	service := newTestService(failingAccountStore{memoryStore: newMemoryStore(), err: want}, memoryAdmins{})
	_, err := service.ActiveForAccount(context.Background(), uuid.New())
	if !errors.Is(err, want) {
		t.Fatalf("active error = %v, want %v", err, want)
	}
}

func TestActiveGlobalReportsAStoreError(t *testing.T) {
	t.Parallel()

	want := errors.New("global features unavailable")
	service := newTestService(failingGlobalStore{memoryStore: newMemoryStore(), err: want}, memoryAdmins{})
	_, err := service.ActiveGlobal(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("active error = %v, want %v", err, want)
	}
}

func flagEnabled(t *testing.T, view List, key string) bool {
	t.Helper()
	for _, flag := range view.Features {
		if flag.Key == key {
			return flag.Enabled
		}
	}
	t.Fatalf("missing %s", key)
	return false
}

type scopeAdmins struct {
	allow uuid.UUID
	calls int
}

func (a *scopeAdmins) Contains(_ context.Context, userID uuid.UUID) (bool, error) {
	a.calls++
	return userID == a.allow && a.allow != uuid.Nil, nil
}

type scopeAccounts struct {
	found map[uuid.UUID]struct{}
	calls int
}

func (a *scopeAccounts) FindByID(_ context.Context, id uuid.UUID) (*models.Account, error) {
	a.calls++
	if _, ok := a.found[id]; !ok {
		return nil, models.ErrRepositoryNotFound
	}
	return &models.Account{ID: id}, nil
}

func TestListScopedForPlatformRefusesOtherScopesBeforeTheAdminCheck(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	admins := &scopeAdmins{allow: uuid.New()}
	accounts := &scopeAccounts{found: map[uuid.UUID]struct{}{}}
	service := newTestService(store, admins)
	accountID := uuid.New()

	for _, scope := range []string{"global", "user", "chain", "account-extra", ""} {
		_, err := service.ListScopedForPlatform(context.Background(), admins.allow, scope, accountID.String(), accounts)
		if !errors.Is(err, ErrScopeNotFound) {
			t.Fatalf("scope %q error = %v", scope, err)
		}
	}
	if admins.calls != 0 || accounts.calls != 0 {
		t.Fatalf("refused scope checked admin %d times and accounts %d times", admins.calls, accounts.calls)
	}
	if len(store.rows) != 0 || len(store.global) != 0 {
		t.Fatal("refused scope wrote a flag row")
	}
}

func TestListScopedForPlatformReadsTheAccountRowAndNotTheGlobalVeto(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	adminID := uuid.New()
	admins := &scopeAdmins{allow: adminID}
	accountID := uuid.New()
	accounts := &scopeAccounts{found: map[uuid.UUID]struct{}{accountID: {}}}
	service := newTestService(store, admins)
	ctx := context.Background()

	_, err := service.ListScopedForPlatform(ctx, uuid.New(), ScopeAccount, accountID.String(), accounts)
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin error = %v", err)
	}
	if accounts.calls != 0 {
		t.Fatal("non-admin read the account")
	}

	_, err = service.ListScopedForPlatform(ctx, adminID, ScopeAccount, "not-a-uuid", accounts)
	if !errors.Is(err, ErrInvalidAccountID) {
		t.Fatalf("bad id error = %v", err)
	}
	if admins.calls != 1 {
		t.Fatalf("bad id checked admin %d times", admins.calls)
	}

	missing := uuid.New()
	_, err = service.ListScopedForPlatform(ctx, adminID, ScopeAccount, missing.String(), accounts)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account error = %v", err)
	}

	if err := store.UpsertGlobal(ctx, FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("global: %v", err)
	}
	view, err := service.ListScopedForPlatform(ctx, adminID, ScopeAccount, accountID.String(), accounts)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !flagEnabled(t, view, FlagWithdrawalsEnabled) {
		t.Fatal("a closed global row changed the account default")
	}
	if len(store.rows[accountID]) != 0 {
		t.Fatal("list inserted an account row")
	}

	if err := store.Upsert(ctx, accountID, FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("account row: %v", err)
	}
	view, err = service.ListScopedForPlatform(ctx, adminID, ScopeAccount, accountID.String(), accounts)
	if err != nil {
		t.Fatalf("stored list: %v", err)
	}
	if flagEnabled(t, view, FlagWithdrawalsEnabled) {
		t.Fatal("stored false read back as true")
	}
	if flagEnabled(t, view, FlagAPIRequestSignatureRequired) {
		t.Fatal("unset flag is not the catalog default")
	}
}

type recordingFeatureActivity struct {
	rows []models.AccountActivity
}

func (a *recordingFeatureActivity) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("activity callback is required")
	}
	return fn(ctx)
}

func (a *recordingFeatureActivity) Append(_ context.Context, row models.AccountActivity) error {
	copied := row
	if row.AccountID != nil {
		id := *row.AccountID
		copied.AccountID = &id
	}
	a.rows = append(a.rows, copied)
	return nil
}

func TestSetScopedForPlatformRefusesOtherScopesBeforeTheAdminCheck(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	admins := &scopeAdmins{allow: uuid.New()}
	accounts := &scopeAccounts{found: map[uuid.UUID]struct{}{}}
	service := newTestService(store, admins)
	accountID := uuid.New()
	writes := []ScopedWrite{{Key: FlagWithdrawalsEnabled, Enabled: false}}

	for _, scope := range []string{"global", "user", "chain", "account-extra", ""} {
		_, err := service.SetScopedForPlatform(context.Background(), admins.allow, scope, accountID.String(), writes, accounts)
		if !errors.Is(err, ErrScopeNotFound) {
			t.Fatalf("scope %q error = %v", scope, err)
		}
	}
	if admins.calls != 0 || accounts.calls != 0 {
		t.Fatalf("refused scope checked admin %d times and accounts %d times", admins.calls, accounts.calls)
	}
	if len(store.rows) != 0 || len(store.global) != 0 {
		t.Fatal("refused scope wrote a flag row")
	}
}

func TestSetScopedForPlatformWritesTheAccountRowAndNotTheGlobalVeto(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	adminID := uuid.New()
	admins := &scopeAdmins{allow: adminID}
	accountID := uuid.New()
	accounts := &scopeAccounts{found: map[uuid.UUID]struct{}{accountID: {}}}
	activity := &recordingFeatureActivity{}
	service := NewService(store, admins, activity)
	ctx := context.Background()
	one := []ScopedWrite{{Key: FlagWithdrawalsEnabled, Enabled: true}}

	_, err := service.SetScopedForPlatform(ctx, uuid.New(), ScopeAccount, accountID.String(), one, accounts)
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin error = %v", err)
	}
	if accounts.calls != 0 || len(store.rows) != 0 {
		t.Fatal("non-admin read the account or wrote a row")
	}

	_, err = service.SetScopedForPlatform(ctx, adminID, ScopeAccount, "not-a-uuid", one, accounts)
	if !errors.Is(err, ErrInvalidAccountID) {
		t.Fatalf("bad id error = %v", err)
	}
	if admins.calls != 1 {
		t.Fatalf("bad id checked admin %d times", admins.calls)
	}

	_, err = service.SetScopedForPlatform(ctx, uuid.New(), ScopeAccount, accountID.String(), []ScopedWrite{{Key: "not-a-flag", Enabled: false}}, accounts)
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("non-admin unknown key error = %v", err)
	}
	if accounts.calls != 0 {
		t.Fatal("non-admin unknown key read the account")
	}

	missing := uuid.New()
	_, err = service.SetScopedForPlatform(ctx, adminID, ScopeAccount, missing.String(), one, accounts)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account error = %v", err)
	}
	if len(store.rows) != 0 {
		t.Fatal("missing account wrote a row")
	}

	_, err = service.SetScopedForPlatform(ctx, adminID, ScopeAccount, accountID.String(), []ScopedWrite{{Key: "not-a-flag", Enabled: false}, {Key: FlagSweepEnabled, Enabled: false}}, accounts)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key error = %v", err)
	}
	if len(store.rows[accountID]) != 0 {
		t.Fatal("unknown key wrote a row before the valid one")
	}

	_, err = service.SetScopedForPlatform(ctx, adminID, ScopeAccount, accountID.String(), []ScopedWrite{{Key: FlagSweepEnabled, Enabled: false}, {Key: FlagSweepEnabled, Enabled: true}}, accounts)
	if !errors.Is(err, ErrDuplicateWrite) {
		t.Fatalf("duplicate error = %v", err)
	}
	if len(store.rows[accountID]) != 0 {
		t.Fatal("duplicate key wrote a row")
	}

	if err := store.UpsertGlobal(ctx, FlagWithdrawalsEnabled, false); err != nil {
		t.Fatalf("global: %v", err)
	}
	written, err := service.SetScopedForPlatform(ctx, adminID, ScopeAccount, accountID.String(), one, accounts)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(written.Features) != 1 || written.Features[0].Key != FlagWithdrawalsEnabled || !written.Features[0].Enabled {
		t.Fatalf("write response = %+v", written.Features)
	}
	if value, ok := store.written(accountID, FlagWithdrawalsEnabled); !ok || !value {
		t.Fatal("account row was not stored true")
	}
	if len(store.rows[accountID]) != 1 {
		t.Fatalf("write inserted %d account rows", len(store.rows[accountID]))
	}
	if value, ok := store.globalWritten(FlagWithdrawalsEnabled); !ok || value {
		t.Fatal("account write changed the closed global row")
	}
	if len(activity.rows) != 1 || activity.rows[0].Action != "features.updated" || activity.rows[0].TargetID != FlagWithdrawalsEnabled {
		t.Fatalf("activity = %+v", activity.rows)
	}
	if activity.rows[0].AccountID == nil || *activity.rows[0].AccountID != accountID {
		t.Fatal("activity row is missing the account id")
	}

	bulk, err := service.SetScopedForPlatform(ctx, adminID, ScopeAccount, accountID.String(), []ScopedWrite{
		{Key: FlagWalletCreationEnabled, Enabled: false},
		{Key: FlagSweepEnabled, Enabled: false},
	}, accounts)
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if len(bulk.Features) != 2 || bulk.Features[0].Key != FlagSweepEnabled || bulk.Features[0].Enabled || bulk.Features[1].Key != FlagWalletCreationEnabled || bulk.Features[1].Enabled {
		t.Fatalf("bulk response = %+v", bulk.Features)
	}
	if len(store.rows[accountID]) != 3 {
		t.Fatalf("bulk account rows = %d", len(store.rows[accountID]))
	}
	if value, ok := store.written(accountID, FlagWithdrawalsEnabled); !ok || !value {
		t.Fatal("bulk rewrite cleared the earlier account row")
	}
}
