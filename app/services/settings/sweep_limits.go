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
// A stored row wins over the registry default. A missing or invalid value
// for one key falls back to that key's default. The platform group named by
// Inherits is not read: that group is not in the registry yet.
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
	return parseSweepLimits(accountID, effectiveNonSecrets(group.Settings, stored, nil)), nil
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
