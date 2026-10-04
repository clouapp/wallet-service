package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// SweepLimitValues is the effective account_sweep_limits document.
// A blank DailyWithdrawCapUSD means the account has no cap.
type SweepLimitValues struct {
	MaxAddressesEVM              int
	MaxAddressesSolana           int
	MaxAddressesBitcoin          int
	MaxConsolidateRequestsPerDay int
	DailyWithdrawCapUSD          string
}

// DefaultSweepLimits is the registry default for an account with no stored row.
func DefaultSweepLimits() SweepLimitValues {
	return SweepLimitValues{
		MaxAddressesEVM:              defaultMaxAddressesEVM,
		MaxAddressesSolana:           defaultMaxAddressesSolana,
		MaxAddressesBitcoin:          defaultMaxAddressesBitcoin,
		MaxConsolidateRequestsPerDay: defaultMaxConsolidateRequestsPerDay,
	}
}

// EffectiveSweepLimits reads account_sweep_limits at the moment of use.
// A sealed cache hit skips the database. A cache miss, a cache failure, or
// a bad seal reads the database. Resolution is the fallback chain: a stored
// account key, then the same key on the platform sweep_limits group, then
// that key's registry default. The account row wins. A missing platform row,
// an invalid platform value, or a failed platform read keeps the registry
// default for that key and does not drop an account override. An invalid
// account value also keeps the registry default for that key. A failed
// account read is returned.
func (s *Service) EffectiveSweepLimits(ctx context.Context, accountID uuid.UUID) (SweepLimitValues, error) {
	if s == nil {
		return SweepLimitValues{}, errServiceRequired
	}
	if err := requireAccount(ctx, accountID); err != nil {
		return SweepLimitValues{}, err
	}
	group, ok := FindGroup(groupAccountSweepLimits)
	if !ok {
		return SweepLimitValues{}, ErrGroupNotFound
	}
	stored, err := s.storedValues(ctx, accountID, group.Name)
	if err != nil {
		return SweepLimitValues{}, err
	}
	merged, err := s.withInheritedPlatform(ctx, group, stored)
	if err != nil {
		return SweepLimitValues{}, err
	}
	return parseSweepLimits(accountID, effectiveNonSecrets(group.Settings, merged, nil)), nil
}

// sweepLimitsWire is the JSON text stored in the dashboard sweep_limits string.
// Field order is the registry order. A blank cap is omitted (unlimited).
type sweepLimitsWire struct {
	MaxAddressesEVM              int    `json:"max_addresses_evm"`
	MaxAddressesSolana           int    `json:"max_addresses_solana"`
	MaxAddressesBitcoin          int    `json:"max_addresses_bitcoin"`
	MaxConsolidateRequestsPerDay int    `json:"max_consolidate_requests_per_day"`
	DailyWithdrawCapUSD          string `json:"daily_withdraw_cap_usd,omitempty"`
}

// AccountSweepLimitsWire is the dashboard sweep_limits string. Nil means the
// account stored no account_sweep_limits row, so the field stays omitted and
// the HTTP contract snapshot is unchanged. When the account stored at least
// one key, the string is the effective document: that key, then the registry
// default for every key the account did not store. A negative amount is refused.
func (s *Service) AccountSweepLimitsWire(ctx context.Context, accountID uuid.UUID) (*string, error) {
	if s == nil {
		return nil, errServiceRequired
	}
	if err := requireAccount(ctx, accountID); err != nil {
		return nil, err
	}
	stored, err := s.storedValues(ctx, accountID, groupAccountSweepLimits)
	if err != nil {
		return nil, err
	}
	if len(stored) == 0 {
		return nil, nil
	}
	effective, err := s.EffectiveSweepLimits(ctx, accountID)
	if err != nil {
		return nil, err
	}
	document, err := marshalSweepLimitsWire(effective)
	if err != nil {
		return nil, err
	}
	return &document, nil
}

func marshalSweepLimitsWire(values SweepLimitValues) (string, error) {
	if values.MaxAddressesEVM <= 0 || values.MaxAddressesSolana <= 0 ||
		values.MaxAddressesBitcoin <= 0 || values.MaxConsolidateRequestsPerDay <= 0 {
		return "", fmt.Errorf("account sweep limits must be greater than 0")
	}
	capUSD := strings.TrimSpace(values.DailyWithdrawCapUSD)
	if capUSD != "" {
		parsed, ok := new(big.Rat).SetString(capUSD)
		if !ok || parsed.Sign() < 0 {
			return "", fmt.Errorf("daily_withdraw_cap_usd must be greater than or equal to 0")
		}
	}
	raw, err := json.Marshal(sweepLimitsWire{
		MaxAddressesEVM:              values.MaxAddressesEVM,
		MaxAddressesSolana:           values.MaxAddressesSolana,
		MaxAddressesBitcoin:          values.MaxAddressesBitcoin,
		MaxConsolidateRequestsPerDay: values.MaxConsolidateRequestsPerDay,
		DailyWithdrawCapUSD:          capUSD,
	})
	if err != nil {
		return "", fmt.Errorf("encode account sweep limits: %w", err)
	}
	return string(raw), nil
}

// withInheritedPlatform copies the platform parent under keys the account did
// not store. The account map is left unchanged: it may be the sealed cache.
// A failed platform read is logged and left out, so those keys stay on the
// registry default. The log names the group, never a stored value.
func (s *Service) withInheritedPlatform(ctx context.Context, group Group, accountStored map[string]string) (map[string]string, error) {
	merged := make(map[string]string, len(group.Settings))
	parentName := strings.TrimSpace(group.Inherits)
	if parentName != "" {
		parent, ok := FindGroup(parentName)
		if ok && parent.Scope == ScopePlatform {
			platform, err := s.platformValues(ctx, parent.Name)
			if err != nil {
				slog.Warn("platform settings fell back to the registry default",
					"group", parent.Name, "error", err)
			} else {
				for _, definition := range group.Settings {
					if value, present := platform[definition.Key]; present {
						merged[definition.Key] = value
					}
				}
			}
		}
	}
	for key, value := range accountStored {
		merged[key] = value
	}
	return merged, nil
}

func parseSweepLimits(accountID uuid.UUID, effective map[string]string) SweepLimitValues {
	defaults := DefaultSweepLimits()
	return SweepLimitValues{
		MaxAddressesEVM:              positiveIntSetting(accountID, keyMaxAddressesEVM, effective[keyMaxAddressesEVM], defaults.MaxAddressesEVM),
		MaxAddressesSolana:           positiveIntSetting(accountID, keyMaxAddressesSolana, effective[keyMaxAddressesSolana], defaults.MaxAddressesSolana),
		MaxAddressesBitcoin:          positiveIntSetting(accountID, keyMaxAddressesBitcoin, effective[keyMaxAddressesBitcoin], defaults.MaxAddressesBitcoin),
		MaxConsolidateRequestsPerDay: positiveIntSetting(accountID, keyMaxConsolidateRequestsPerDay, effective[keyMaxConsolidateRequestsPerDay], defaults.MaxConsolidateRequestsPerDay),
		DailyWithdrawCapUSD:          decimalSetting(accountID, keyDailyWithdrawCapUSD, effective[keyDailyWithdrawCapUSD]),
	}
}

func positiveIntSetting(accountID uuid.UUID, key, raw string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		slog.Warn("account sweep limit fell back to the registry default",
			"account_id", accountID, "key", key)
		return fallback
	}
	return parsed
}

func decimalSetting(accountID uuid.UUID, key, raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	parsed, ok := new(big.Rat).SetString(text)
	if !ok || parsed.Sign() < 0 {
		slog.Warn("account sweep limit fell back to no cap",
			"account_id", accountID, "key", key)
		return ""
	}
	return text
}
