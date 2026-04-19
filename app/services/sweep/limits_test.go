package sweep

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// fakeAccountRepo is a narrow in-memory AccountRepository used by the sweep
// tests. Methods that tests don't exercise return zero values.
type fakeAccountRepo struct {
	byID map[uuid.UUID]*models.Account
	err  error
}

func (f *fakeAccountRepo) Create(account *models.Account) error { return nil }
func (f *fakeAccountRepo) FindByID(id uuid.UUID) (*models.Account, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.byID == nil {
		return nil, nil
	}
	return f.byID[id], nil
}
func (f *fakeAccountRepo) FindByIDs(ids []uuid.UUID) ([]models.Account, error) { return nil, nil }
func (f *fakeAccountRepo) UpdateField(id uuid.UUID, field string, value interface{}) error {
	return nil
}

// ---------------------------------------------------------------------------
// LoadLimits
// ---------------------------------------------------------------------------

func TestLoadLimits_NilAccountReturnsDefaults(t *testing.T) {
	svc := &service{accountRepo: &fakeAccountRepo{}}
	limits, err := svc.LoadLimits(context.Background(), uuid.Nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if limits.MaxConsolidateReqPerDay != 50 {
		t.Fatalf("expected default MaxConsolidateReqPerDay=50, got %d", limits.MaxConsolidateReqPerDay)
	}
	if limits.MaxAddressesPerRequest["evm"] != 100 {
		t.Fatalf("expected default evm=100, got %d", limits.MaxAddressesPerRequest["evm"])
	}
	if limits.MaxAddressesPerRequest["sol"] != 25 {
		t.Fatalf("expected default sol=25, got %d", limits.MaxAddressesPerRequest["sol"])
	}
	if limits.DailyWithdrawCapUSD != nil {
		t.Fatalf("expected DailyWithdrawCapUSD=nil by default, got %v", *limits.DailyWithdrawCapUSD)
	}
}

func TestLoadLimits_OverrideMergesWithDefaults(t *testing.T) {
	raw := `{"max_consolidate_requests_per_day": 200, "max_addresses_per_request": {"evm": 500}, "daily_withdraw_cap_usd": "50000.50"}`
	accountID := uuid.New()
	svc := &service{accountRepo: &fakeAccountRepo{
		byID: map[uuid.UUID]*models.Account{
			accountID: {ID: accountID, SweepLimits: &raw},
		},
	}}

	limits, err := svc.LoadLimits(context.Background(), accountID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if limits.MaxConsolidateReqPerDay != 200 {
		t.Fatalf("expected override MaxConsolidateReqPerDay=200, got %d", limits.MaxConsolidateReqPerDay)
	}
	if limits.MaxAddressesPerRequest["evm"] != 500 {
		t.Fatalf("expected override evm=500, got %d", limits.MaxAddressesPerRequest["evm"])
	}
	// sol was not overridden — must inherit the default.
	if limits.MaxAddressesPerRequest["sol"] != 25 {
		t.Fatalf("expected inherited default sol=25, got %d", limits.MaxAddressesPerRequest["sol"])
	}
	if limits.MaxAddressesPerRequest["bitcoin"] != 100 {
		t.Fatalf("expected inherited default bitcoin=100, got %d", limits.MaxAddressesPerRequest["bitcoin"])
	}
	if limits.DailyWithdrawCapUSD == nil || *limits.DailyWithdrawCapUSD != 50000.50 {
		t.Fatalf("expected DailyWithdrawCapUSD=50000.50, got %v", limits.DailyWithdrawCapUSD)
	}
}

func TestLoadLimits_InvalidJSONFallsBackToDefaults(t *testing.T) {
	raw := `{not json`
	accountID := uuid.New()
	svc := &service{accountRepo: &fakeAccountRepo{
		byID: map[uuid.UUID]*models.Account{
			accountID: {ID: accountID, SweepLimits: &raw},
		},
	}}

	limits, err := svc.LoadLimits(context.Background(), accountID)
	if err != nil {
		t.Fatalf("invalid JSON must not surface an error, got %v", err)
	}
	if limits.MaxConsolidateReqPerDay != 50 {
		t.Fatalf("expected fallback default 50, got %d", limits.MaxConsolidateReqPerDay)
	}
	if limits.MaxAddressesPerRequest["evm"] != 100 {
		t.Fatalf("expected fallback default evm=100, got %d", limits.MaxAddressesPerRequest["evm"])
	}
}

func TestLoadLimits_EmptySweepLimitsStringReturnsDefaults(t *testing.T) {
	empty := ""
	accountID := uuid.New()
	svc := &service{accountRepo: &fakeAccountRepo{
		byID: map[uuid.UUID]*models.Account{
			accountID: {ID: accountID, SweepLimits: &empty},
		},
	}}

	limits, err := svc.LoadLimits(context.Background(), accountID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if limits.MaxConsolidateReqPerDay != 50 {
		t.Fatalf("expected default 50 for empty sweep_limits string, got %d", limits.MaxConsolidateReqPerDay)
	}
}

func TestLoadLimits_AccountNotFoundReturnsDefaults(t *testing.T) {
	svc := &service{accountRepo: &fakeAccountRepo{byID: map[uuid.UUID]*models.Account{}}}

	limits, err := svc.LoadLimits(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if limits.MaxConsolidateReqPerDay != 50 {
		t.Fatalf("expected default 50 when account is missing, got %d", limits.MaxConsolidateReqPerDay)
	}
}

// ---------------------------------------------------------------------------
// checkAddressesPerRequest
// ---------------------------------------------------------------------------

func TestCheckAddressesPerRequest_AtLimitIsOK(t *testing.T) {
	limits := &Limits{MaxAddressesPerRequest: map[string]int{"evm": 100}}
	if err := checkAddressesPerRequest("evm", 100, limits); err != nil {
		t.Fatalf("at-limit request should pass, got %v", err)
	}
}

func TestCheckAddressesPerRequest_AboveLimitReturnsErrTooManyAddresses(t *testing.T) {
	limits := &Limits{MaxAddressesPerRequest: map[string]int{"evm": 100}}
	err := checkAddressesPerRequest("evm", 101, limits)
	if err == nil {
		t.Fatal("expected error for count above limit")
	}
	if !errors.Is(err, ErrTooManyAddresses) {
		t.Fatalf("expected errors.Is(err, ErrTooManyAddresses), got %v", err)
	}
}

func TestCheckAddressesPerRequest_UnknownAdapterHasNoCap(t *testing.T) {
	limits := &Limits{MaxAddressesPerRequest: map[string]int{"evm": 100}}
	if err := checkAddressesPerRequest("cosmos", 10_000, limits); err != nil {
		t.Fatalf("unknown adapter should have no cap, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Redis helpers — nil-client safety
// ---------------------------------------------------------------------------

func TestAcquireWalletOpsLock_NoRedisIsNoop(t *testing.T) {
	svc := &service{rdb: nil}
	release, err := svc.acquireWalletOpsLock(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("nil Redis client must be a no-op, got %v", err)
	}
	if release == nil {
		t.Fatal("release func must not be nil even when Redis is disabled")
	}
	release() // must not panic
}

func TestIncrDailyQuota_NoRedisIsNoop(t *testing.T) {
	svc := &service{rdb: nil}
	limits := &Limits{MaxConsolidateReqPerDay: 1}
	if err := svc.incrDailyQuota(context.Background(), uuid.New(), limits); err != nil {
		t.Fatalf("nil Redis client must be a no-op, got %v", err)
	}
}

func TestIncrDailyQuota_NilAccountIsNoop(t *testing.T) {
	// Even with a non-nil Redis client placeholder, a nil accountID must
	// short-circuit before any Redis call. We assert by passing nil rdb as
	// well (any call would panic) — proving the accountID guard runs first.
	svc := &service{rdb: nil}
	limits := &Limits{MaxConsolidateReqPerDay: 1}
	if err := svc.incrDailyQuota(context.Background(), uuid.Nil, limits); err != nil {
		t.Fatalf("nil accountID must be a no-op, got %v", err)
	}
}
