package sweep

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/cacheguard"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/pkg/numeric"
)

// LoadLimits returns the effective sweep limits for an account. The reader
// applies a stored account_sweep_limits row over the platform sweep_limits
// row, and either missing layer keeps the registry default. A missing source,
// a nil account, or a failed account read also keeps the registry defaults:
// limits are best-effort config, and a settings outage must not block a
// withdrawal. uuid.Nil never queries settings. A blank daily cap is unlimited.
func (s *service) LoadLimits(ctx context.Context, accountID uuid.UUID) (*Limits, error) {
	values := settings.DefaultSweepLimits()
	if accountID != uuid.Nil && s.sweepLimits != nil {
		loaded, err := s.sweepLimits(ctx, accountID)
		if err != nil {
			slog.Warn("load account sweep limits", "account_id", accountID, "error", err)
		} else {
			values = loaded
		}
	}
	return limitsFromSettings(accountID, values), nil
}

func limitsFromSettings(accountID uuid.UUID, values settings.SweepLimitValues) *Limits {
	limits := &Limits{
		MaxAddressesPerRequest: map[string]int{
			models.AdapterTypeEVM:     values.MaxAddressesEVM,
			models.AdapterTypeSolana:  values.MaxAddressesSolana,
			models.AdapterTypeBitcoin: values.MaxAddressesBitcoin,
			// Each TRON leg can wait a block for its gas_seed.
			models.AdapterTypeTron: 50,
		},
		MaxConsolidateReqPerDay: values.MaxConsolidateRequestsPerDay,
	}
	capUSD := strings.TrimSpace(values.DailyWithdrawCapUSD)
	if capUSD == "" {
		return limits
	}
	parsed, err := numeric.ParseNonNegative("daily_withdraw_cap_usd", capUSD)
	if err != nil {
		slog.Warn("parse account sweep limit daily_withdraw_cap_usd",
			"account_id", accountID, "error", err)
		return limits
	}
	limits.DailyWithdrawCapUSD = &parsed
	return limits
}

// acquireWalletOpsLock takes a short-lived cache mutex keyed by walletID so
// that only one in-flight withdrawal/consolidation touches a given wallet's
// addresses at a time. Without a cache the lock cannot be taken, so it refuses;
// so it does when the cache is down, which is not reported as a busy wallet.
func (s *service) acquireWalletOpsLock(walletID uuid.UUID) (func(), error) {
	if s.cache == nil {
		return nil, ErrCacheUnavailable
	}
	key := "vault:lock:wallet_ops:" + walletID.String()
	ok, err := cacheguard.Acquire(s.cache, key, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("acquire wallet ops lock: %w", err)
	}
	if !ok {
		return nil, ErrInFlightConsolidation
	}
	return func() { s.cache.Forget(key) }, nil
}

// incrDailyQuota atomically increments the per-account daily consolidate
// counter and returns ErrDailyQuotaExceeded once the limit is crossed. The
// first increment of a given UTC day sets a 24h TTL so the key self-expires.
// A nil accountID has nothing to meter; without a cache it refuses.
func (s *service) incrDailyQuota(accountID uuid.UUID, limits *Limits) error {
	if accountID == uuid.Nil {
		return nil
	}
	if s.cache == nil {
		return ErrCacheUnavailable
	}
	key := fmt.Sprintf("vault:quota:consolidate:%s:%s",
		accountID.String(), time.Now().UTC().Format("2006-01-02"))
	n, err := cacheguard.Count(s.cache, key, 1, 24*time.Hour)
	if err != nil {
		return fmt.Errorf("incr daily quota: %w", err)
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
