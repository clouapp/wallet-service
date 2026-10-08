package sweep

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/settings"
)

// ---------------------------------------------------------------------------
// LoadLimits
// ---------------------------------------------------------------------------

func TestLoad_Limits_NilAccountReturnsDefaultsWithoutReading(t *testing.T) {
	svc := &service{sweepLimits: func(context.Context, uuid.UUID) (settings.SweepLimitValues, error) {
		t.Fatal("nil account must not read settings")
		return settings.SweepLimitValues{}, nil
	}}
	limits, err := svc.LoadLimits(context.Background(), uuid.Nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertDefaultLimits(t, limits)
}

func TestLoad_Limits_NilSourceReturnsDefaults(t *testing.T) {
	limits, err := (&service{}).LoadLimits(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertDefaultLimits(t, limits)
}

func TestLoad_Limits_AppliesStoredSweepLimits(t *testing.T) {
	accountID := uuid.New()
	svc := &service{sweepLimits: func(_ context.Context, id uuid.UUID) (settings.SweepLimitValues, error) {
		if id != accountID {
			t.Fatalf("read limits for %s, want %s", id, accountID)
		}
		return settings.SweepLimitValues{
			MaxAddressesEVM:              500,
			MaxAddressesSolana:           25,
			MaxAddressesBitcoin:          100,
			MaxConsolidateRequestsPerDay: 200,
			DailyWithdrawCapUSD:          "50000.50",
		}, nil
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
	if limits.MaxAddressesPerRequest[models.AdapterTypeSolana] != 25 {
		t.Fatalf("expected solana=25, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeSolana])
	}
	if limits.MaxAddressesPerRequest[models.AdapterTypeBitcoin] != 100 {
		t.Fatalf("expected bitcoin=100, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	}
	if limits.DailyWithdrawCapUSD == nil || !limits.DailyWithdrawCapUSD.Equal(decimal.RequireFromString("50000.50")) {
		t.Fatalf("expected DailyWithdrawCapUSD=50000.50, got %v", limits.DailyWithdrawCapUSD)
	}
}

func TestLoad_Limits_ReadErrorReturnsDefaults(t *testing.T) {
	svc := &service{sweepLimits: func(context.Context, uuid.UUID) (settings.SweepLimitValues, error) {
		return settings.SweepLimitValues{}, errors.New("settings unavailable")
	}}

	limits, err := svc.LoadLimits(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("a settings outage must not surface an error, got %v", err)
	}
	assertDefaultLimits(t, limits)
}

func TestLoad_Limits_BlankCapStaysUnlimited(t *testing.T) {
	svc := &service{sweepLimits: func(context.Context, uuid.UUID) (settings.SweepLimitValues, error) {
		values := settings.DefaultSweepLimits()
		values.DailyWithdrawCapUSD = "   "
		return values, nil
	}}
	limits, err := svc.LoadLimits(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if limits.DailyWithdrawCapUSD != nil {
		t.Fatalf("blank cap must stay unlimited, got %v", *limits.DailyWithdrawCapUSD)
	}
}

func assertDefaultLimits(t *testing.T, limits *Limits) {
	t.Helper()
	if limits.MaxConsolidateReqPerDay != 50 {
		t.Fatalf("expected default MaxConsolidateReqPerDay=50, got %d", limits.MaxConsolidateReqPerDay)
	}
	if limits.MaxAddressesPerRequest[models.AdapterTypeEVM] != 100 {
		t.Fatalf("expected default evm=100, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeEVM])
	}
	if limits.MaxAddressesPerRequest[models.AdapterTypeSolana] != 25 {
		t.Fatalf("expected default solana=25, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeSolana])
	}
	if limits.MaxAddressesPerRequest[models.AdapterTypeBitcoin] != 100 {
		t.Fatalf("expected default bitcoin=100, got %d", limits.MaxAddressesPerRequest[models.AdapterTypeBitcoin])
	}
	if limits.DailyWithdrawCapUSD != nil {
		t.Fatalf("expected DailyWithdrawCapUSD=nil by default, got %v", *limits.DailyWithdrawCapUSD)
	}
}

// ---------------------------------------------------------------------------
// checkAddressesPerRequest
// ---------------------------------------------------------------------------

func TestCheck_AddressesPerRequest_AtLimitIsOK(t *testing.T) {
	limits := &Limits{MaxAddressesPerRequest: map[string]int{models.AdapterTypeEVM: 100}}
	if err := checkAddressesPerRequest(models.AdapterTypeEVM, 100, limits); err != nil {
		t.Fatalf("at-limit request should pass, got %v", err)
	}
}

func TestCheck_AddressesPerRequest_AboveLimitReturnsErrTooManyAddresses(t *testing.T) {
	limits := &Limits{MaxAddressesPerRequest: map[string]int{models.AdapterTypeEVM: 100}}
	err := checkAddressesPerRequest(models.AdapterTypeEVM, 101, limits)
	if err == nil {
		t.Fatal("expected error for count above limit")
	}
	if !errors.Is(err, ErrTooManyAddresses) {
		t.Fatalf("expected errors.Is(err, ErrTooManyAddresses), got %v", err)
	}
}

func TestCheck_AddressesPerRequest_UnknownAdapterHasNoCap(t *testing.T) {
	limits := &Limits{MaxAddressesPerRequest: map[string]int{models.AdapterTypeEVM: 100}}
	if err := checkAddressesPerRequest("cosmos", 10_000, limits); err != nil {
		t.Fatalf("unknown adapter should have no cap, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Redis helpers — nil-client safety (no Redis configured)
// ---------------------------------------------------------------------------

func TestAcquireWalletOpsLock_NilRdb_Refuses(t *testing.T) {
	svc := &service{rdb: nil}
	release, err := svc.acquireWalletOpsLock(context.Background(), uuid.New())
	if !errors.Is(err, ErrRedisUnavailable) {
		t.Fatalf("without Redis the wallet lock must refuse, got %v", err)
	}
	if release != nil {
		t.Fatal("no release func expected when the lock was not taken")
	}
}

func TestIncrDailyQuota_NilRdb_Refuses(t *testing.T) {
	svc := &service{rdb: nil}
	limits := &Limits{MaxConsolidateReqPerDay: 1}
	if err := svc.incrDailyQuota(context.Background(), uuid.New(), limits); !errors.Is(err, ErrRedisUnavailable) {
		t.Fatalf("without Redis the daily quota must refuse, got %v", err)
	}
}

func TestIncr_DailyQuota_NilAccountIsNoop(t *testing.T) {
	// There is no account to meter, so the guard runs before any Redis call.
	svc := &service{rdb: nil}
	limits := &Limits{MaxConsolidateReqPerDay: 1}
	if err := svc.incrDailyQuota(context.Background(), uuid.Nil, limits); err != nil {
		t.Fatalf("nil accountID must be a no-op, got %v", err)
	}
}

type failingRedis struct{ err error }

func (f failingRedis) SetNX(context.Context, string, string, time.Duration) (bool, error) {
	return false, f.err
}
func (f failingRedis) Del(context.Context, string) error                   { return f.err }
func (f failingRedis) Incr(context.Context, string) (int64, error)         { return 0, f.err }
func (f failingRedis) Expire(context.Context, string, time.Duration) error { return f.err }

func TestRedisHelpers_RedisError_Refuses(t *testing.T) {
	down := errors.New("down")
	svc := &service{rdb: failingRedis{err: down}}
	if _, err := svc.acquireWalletOpsLock(context.Background(), uuid.New()); !errors.Is(err, down) {
		t.Fatalf("a Redis error must refuse the wallet lock, got %v", err)
	}
	limits := &Limits{MaxConsolidateReqPerDay: 1}
	if err := svc.incrDailyQuota(context.Background(), uuid.New(), limits); !errors.Is(err, down) {
		t.Fatalf("a Redis error must refuse the daily quota, got %v", err)
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
	svc := &service{rdb: newRedisStore()}
	ctx := context.Background()

	walletID := uuid.New()

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
	svc := &service{rdb: newRedisStore()}
	ctx := context.Background()

	accountID := uuid.New()
	limits := &Limits{MaxConsolidateReqPerDay: 3}

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
