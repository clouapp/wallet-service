package sweep

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

// LoadLimits returns the effective sweep limits for an account: hard-coded
// defaults overlaid with any per-account overrides stored in
// accounts.sweep_limits (JSONB). Missing, empty, or invalid overrides fall
// back silently to defaults — limits are best-effort config, not a hard
// dependency, and a malformed row must never break withdrawals.
func (s *service) LoadLimits(ctx context.Context, accountID uuid.UUID) (*Limits, error) {
	defaults := &Limits{
		MaxAddressesPerRequest: map[string]int{
			models.AdapterTypeEVM:     100,
			models.AdapterTypeSolana:  25,
			models.AdapterTypeBitcoin: 100,
		},
		MaxConsolidateReqPerDay: 50,
	}
	if accountID == uuid.Nil {
		return defaults, nil
	}
	account, err := s.accountRepo.FindByID(ctx, accountID)
	if err != nil || account == nil {
		return defaults, nil
	}
	if account.SweepLimits == nil || *account.SweepLimits == "" {
		return defaults, nil
	}

	var override struct {
		MaxAddressesPerRequest  map[string]int `json:"max_addresses_per_request"`
		MaxConsolidateReqPerDay *int           `json:"max_consolidate_requests_per_day"`
		DailyWithdrawCapUSD     *string        `json:"daily_withdraw_cap_usd"`
	}
	if err := json.Unmarshal([]byte(*account.SweepLimits), &override); err != nil {
		slog.Warn("parse account sweep_limits", "account_id", accountID, "error", err)
		return defaults, nil
	}

	merged := &Limits{
		MaxAddressesPerRequest:  map[string]int{},
		MaxConsolidateReqPerDay: defaults.MaxConsolidateReqPerDay,
	}
	for k, v := range defaults.MaxAddressesPerRequest {
		merged.MaxAddressesPerRequest[k] = v
	}
	for k, v := range override.MaxAddressesPerRequest {
		merged.MaxAddressesPerRequest[k] = v
	}
	if override.MaxConsolidateReqPerDay != nil {
		merged.MaxConsolidateReqPerDay = *override.MaxConsolidateReqPerDay
	}
	if override.DailyWithdrawCapUSD != nil {
		if f, err := strconv.ParseFloat(*override.DailyWithdrawCapUSD, 64); err == nil {
			merged.DailyWithdrawCapUSD = &f
		} else {
			slog.Warn("parse account sweep_limits.daily_withdraw_cap_usd",
				"account_id", accountID, "value", *override.DailyWithdrawCapUSD, "error", err)
		}
	}
	return merged, nil
}

// acquireWalletOpsLock takes a short-lived Redis mutex keyed by walletID so
// that only one in-flight withdrawal/consolidation touches a given wallet's
// addresses at a time. When Redis is not configured (s.rdb == nil) the lock
// is a no-op — safe for unit tests and dev runs without a Redis instance.
func (s *service) acquireWalletOpsLock(ctx context.Context, walletID uuid.UUID) (func(), error) {
	if s.rdb == nil {
		return func() {}, nil
	}
	key := "vault:lock:wallet_ops:" + walletID.String()
	ok, err := s.rdb.SetNX(ctx, key, "1", 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("acquire wallet ops lock: %w", err)
	}
	if !ok {
		return nil, ErrInFlightConsolidation
	}
	return func() {
		// Use a background context for release so an already-cancelled
		// request context cannot leak the lock until its 60s TTL.
		_ = s.rdb.Del(context.Background(), key)
	}, nil
}

// incrDailyQuota atomically increments the per-account daily consolidate
// counter in Redis and returns ErrDailyQuotaExceeded once the limit is
// crossed. The first increment of a given UTC day sets a 24h TTL so the key
// self-expires. No-op when Redis is not configured or when accountID is nil.
func (s *service) incrDailyQuota(ctx context.Context, accountID uuid.UUID, limits *Limits) error {
	if s.rdb == nil || accountID == uuid.Nil {
		return nil
	}
	key := fmt.Sprintf("vault:quota:consolidate:%s:%s",
		accountID.String(), time.Now().UTC().Format("2006-01-02"))
	n, err := s.rdb.Incr(ctx, key)
	if err != nil {
		return fmt.Errorf("incr daily quota: %w", err)
	}
	if n == 1 {
		_ = s.rdb.Expire(ctx, key, 24*time.Hour)
	}
	if int(n) > limits.MaxConsolidateReqPerDay {
		return ErrDailyQuotaExceeded
	}
	return nil
}

// checkAddressesPerRequest enforces the per-adapter cap on how many wallet
// addresses may participate in a single consolidation/sweep request. An
// unknown adapter type has no cap (returns nil) so new chains don't
// accidentally inherit a restrictive default before limits are tuned.
func checkAddressesPerRequest(adapterType string, count int, limits *Limits) error {
	max, ok := limits.MaxAddressesPerRequest[adapterType]
	if !ok {
		return nil
	}
	if count > max {
		return fmt.Errorf("%w: %d > %d (adapter=%s)", ErrTooManyAddresses, count, max, adapterType)
	}
	return nil
}
