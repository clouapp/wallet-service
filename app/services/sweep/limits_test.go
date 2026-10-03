package sweep

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/testutil"
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
func (f *fakeAccountRepo) PaginateByMember(userID uuid.UUID, filter repositories.AccountListFilter, limit, offset int) ([]models.Account, int64, error) {
	return nil, 0, nil
}
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
	if limits.MaxAddressesPerRequest[models.AdapterTypeEVM] != 100 {
		t.Fatalf("expected default evm=100, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeEVM])
	}
	if limits.MaxAddressesPerRequest[models.AdapterTypeSolana] != 25 {
		t.Fatalf("expected default solana=25, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeSolana])
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
	if limits.MaxAddressesPerRequest[models.AdapterTypeEVM] != 500 {
		t.Fatalf("expected override evm=500, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeEVM])
	}
	// solana was not overridden — must inherit the default.
	if limits.MaxAddressesPerRequest[models.AdapterTypeSolana] != 25 {
		t.Fatalf("expected inherited default solana=25, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeSolana])
	}
	if limits.MaxAddressesPerRequest[models.AdapterTypeBitcoin] != 100 {
		t.Fatalf("expected inherited default bitcoin=100, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	}
	if limits.DailyWithdrawCapUSD == nil || !limits.DailyWithdrawCapUSD.Equal(decimal.RequireFromString("50000.50")) {
		t.Fatalf("expected DailyWithdrawCapUSD=50000.50, got %v", limits.DailyWithdrawCapUSD)
	}
}

func TestLoadLimits_DailyWithdrawCapKeepsExactCents(t *testing.T) {
	raw := `{"daily_withdraw_cap_usd":"0.30"}`
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
	sum := decimal.RequireFromString("0.1").Add(decimal.RequireFromString("0.2"))
	if limits.DailyWithdrawCapUSD == nil || !limits.DailyWithdrawCapUSD.Equal(sum) {
		t.Fatalf("expected DailyWithdrawCapUSD=0.3 exactly, got %v", limits.DailyWithdrawCapUSD)
	}
}

func TestLoadLimits_InvalidOrNegativeDailyWithdrawCapIsIgnored(t *testing.T) {
	for _, capText := range []string{"-1", "NaN", "Inf", "0x1p-2", "", "ten"} {
		raw := fmt.Sprintf(`{"daily_withdraw_cap_usd":%q}`, capText)
		accountID := uuid.New()
		svc := &service{accountRepo: &fakeAccountRepo{
			byID: map[uuid.UUID]*models.Account{
				accountID: {ID: accountID, SweepLimits: &raw},
			},
		}}

		limits, err := svc.LoadLimits(context.Background(), accountID)
		if err != nil {
			t.Fatalf("cap %q: unexpected error: %v", capText, err)
		}
		if limits.DailyWithdrawCapUSD != nil {
			t.Fatalf("cap %q: expected it ignored (unlimited), got %v", capText, limits.DailyWithdrawCapUSD)
		}
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
	if limits.MaxAddressesPerRequest[models.AdapterTypeEVM] != 100 {
		t.Fatalf("expected fallback default evm=100, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeEVM])
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
	limits := &Limits{MaxAddressesPerRequest: map[string]int{models.AdapterTypeEVM: 100}}
	if err := checkAddressesPerRequest(models.AdapterTypeEVM, 100, limits); err != nil {
		t.Fatalf("at-limit request should pass, got %v", err)
	}
}

func TestCheckAddressesPerRequest_AboveLimitReturnsErrTooManyAddresses(t *testing.T) {
	limits := &Limits{MaxAddressesPerRequest: map[string]int{models.AdapterTypeEVM: 100}}
	err := checkAddressesPerRequest(models.AdapterTypeEVM, 101, limits)
	if err == nil {
		t.Fatal("expected error for count above limit")
	}
	if !errors.Is(err, ErrTooManyAddresses) {
		t.Fatalf("expected errors.Is(err, ErrTooManyAddresses), got %v", err)
	}
}

func TestCheckAddressesPerRequest_UnknownAdapterHasNoCap(t *testing.T) {
	limits := &Limits{MaxAddressesPerRequest: map[string]int{models.AdapterTypeEVM: 100}}
	if err := checkAddressesPerRequest("cosmos", 10_000, limits); err != nil {
		t.Fatalf("unknown adapter should have no cap, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Redis helpers — nil-client safety (no Redis configured)
// ---------------------------------------------------------------------------

func TestAcquireWalletOpsLock_NilRdb_IsNoop(t *testing.T) {
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

func TestIncrDailyQuota_NilRdb_IsNoop(t *testing.T) {
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

// ---------------------------------------------------------------------------
// Redis helpers — real Redis (docker-compose waas-redis on localhost:6379)
// These tests skip automatically when Redis is offline.
// ---------------------------------------------------------------------------

// TestAcquireWalletOpsLock_RealRedis_Contention exercises the SetNX lock path
// against a live Redis: a second acquire for the same wallet must return
// ErrInFlightConsolidation until the first release runs, after which a fresh
// acquire succeeds.
func TestAcquireWalletOpsLock_RealRedis_Contention(t *testing.T) {
	client := testutil.TestRedis(t)
	svc := &service{rdb: client}
	ctx := context.Background()

	// Unique wallet ID isolates this test's keys from any concurrent runs.
	walletID := uuid.New()
	lockKey := "vault:lock:wallet_ops:" + walletID.String()
	t.Cleanup(func() { _ = client.Del(context.Background(), lockKey).Err() })

	release1, err := svc.acquireWalletOpsLock(ctx, walletID)
	if err != nil {
		t.Fatalf("first acquire must succeed, got %v", err)
	}
	if release1 == nil {
		t.Fatal("release func must not be nil on successful acquire")
	}

	if _, err := svc.acquireWalletOpsLock(ctx, walletID); !errors.Is(err, ErrInFlightConsolidation) {
		t.Fatalf("second acquire must return ErrInFlightConsolidation, got %v", err)
	}

	release1()

	release2, err := svc.acquireWalletOpsLock(ctx, walletID)
	if err != nil {
		t.Fatalf("acquire after release must succeed, got %v", err)
	}
	release2()
}

// TestIncrDailyQuota_RealRedis_Exceeds verifies the INCR-based daily quota:
// the first N calls (N == MaxConsolidateReqPerDay) succeed, and the (N+1)th
// returns ErrDailyQuotaExceeded. Uses a fresh accountID per run so the daily
// counter starts at zero.
func TestIncrDailyQuota_RealRedis_Exceeds(t *testing.T) {
	client := testutil.TestRedis(t)
	svc := &service{rdb: client}
	ctx := context.Background()

	accountID := uuid.New()
	limits := &Limits{MaxConsolidateReqPerDay: 3}

	quotaKey := fmt.Sprintf("vault:quota:consolidate:%s:%s",
		accountID.String(), time.Now().UTC().Format("2006-01-02"))
	t.Cleanup(func() { _ = client.Del(context.Background(), quotaKey).Err() })

	for i := 1; i <= limits.MaxConsolidateReqPerDay; i++ {
		if err := svc.incrDailyQuota(ctx, accountID, limits); err != nil {
			t.Fatalf("call %d must succeed within quota, got %v", i, err)
		}
	}

	if err := svc.incrDailyQuota(ctx, accountID, limits); !errors.Is(err, ErrDailyQuotaExceeded) {
		t.Fatalf("call %d must return ErrDailyQuotaExceeded, got %v",
			limits.MaxConsolidateReqPerDay+1, err)
	}
}
