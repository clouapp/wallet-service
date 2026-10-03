package settings

import (
	"context"
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
// account key, then the same key on the platform group named by Inherits,
// then that key's registry default. A parent that is not in the registry is
// skipped, so the chain is account then default until sweep_limits exists.
// An invalid stored value falls back to the registry default for that key.
// A database failure is returned.
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

// withInheritedPlatform copies the platform parent under keys the account did
// not store. The account map is left unchanged: it may be the sealed cache.
func (s *Service) withInheritedPlatform(ctx context.Context, group Group, accountStored map[string]string) (map[string]string, error) {
	merged := make(map[string]string, len(group.Settings))
	parentName := strings.TrimSpace(group.Inherits)
	if parentName != "" {
		parent, ok := FindGroup(parentName)
		if ok && parent.Scope == ScopePlatform {
			platform, err := s.platformValues(ctx, parent.Name)
			if err != nil {
				return nil, err
			}
			for _, definition := range group.Settings {
				if value, present := platform[definition.Key]; present {
					merged[definition.Key] = value
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
